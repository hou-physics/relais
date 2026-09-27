// local.go 支持"接入现有对话"（M9）：本地模块登记表、投递记录、
// 频道改名/删除/重开，以及本地回合计数。
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrChannelExists = errors.New("频道名已存在")

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

type LocalModule struct {
	ChannelID                                                           int64
	Name, Dir                                                           string
	ArchivedSeq                                                         int
	CodexThreadID, CodexThreadName, CodexAttachedAt, CodexDeliveryError string
	CreatedAt, ClosedAt                                                 string
}

func (s *Store) UpsertLocalModule(channelID int64, dir string) error {
	_, err := s.db.Exec(`INSERT INTO local_modules (channel_id, dir, created_at) VALUES (?,?,?)
		ON CONFLICT(channel_id) DO UPDATE SET dir=excluded.dir`, channelID, dir, now())
	return err
}

const localModuleCols = `m.channel_id, c.name, m.dir, m.archived_seq, m.codex_thread_id, m.codex_thread_name, m.codex_attached_at, m.codex_delivery_error, m.created_at, m.closed_at`

func scanLocalModule(row interface{ Scan(...any) error }) (LocalModule, error) {
	var m LocalModule
	err := row.Scan(&m.ChannelID, &m.Name, &m.Dir, &m.ArchivedSeq, &m.CodexThreadID, &m.CodexThreadName, &m.CodexAttachedAt, &m.CodexDeliveryError, &m.CreatedAt, &m.ClosedAt)
	return m, err
}

func (s *Store) LocalModules() ([]LocalModule, error) {
	rows, err := s.db.Query(`SELECT ` + localModuleCols + ` FROM local_modules m JOIN channels c ON c.id=m.channel_id ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LocalModule
	for rows.Next() {
		m, err := scanLocalModule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) LocalModuleByName(name string) (LocalModule, error) {
	return scanLocalModule(s.db.QueryRow(`SELECT `+localModuleCols+` FROM local_modules m JOIN channels c ON c.id=m.channel_id WHERE c.name=?`, name))
}

func (s *Store) SetArchivedSeq(channelID int64, seq int) error {
	_, err := s.db.Exec(`UPDATE local_modules SET archived_seq=? WHERE channel_id=?`, seq, channelID)
	return err
}

// SetCodexAttach：登记 codex 侧接入的对话，清掉模块级投递错误，并删掉本频道 codex 侧
// 的失败投递记录——接入变了，之前失败的信应立刻重投，不必等重试间隔（终审修复 #2）。
func (s *Store) SetCodexAttach(channelID int64, threadID, threadName, at string) error {
	if _, err := s.db.Exec(`UPDATE local_modules SET codex_thread_id=?, codex_thread_name=?, codex_attached_at=?, codex_delivery_error='' WHERE channel_id=?`, threadID, threadName, at, channelID); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM local_deliveries WHERE side='codex' AND status='error' AND message_id IN (SELECT id FROM messages WHERE channel_id=?)`, channelID)
	return err
}

func (s *Store) SetCodexDeliveryError(channelID int64, msg string) error {
	_, err := s.db.Exec(`UPDATE local_modules SET codex_delivery_error=? WHERE channel_id=?`, msg, channelID)
	return err
}

func (s *Store) SetLocalClosed(channelID int64, closedAt string) error {
	_, err := s.db.Exec(`UPDATE local_modules SET closed_at=? WHERE channel_id=?`, closedAt, channelID)
	return err
}

func (s *Store) RecordDelivery(messageID, side, status, errText string) error {
	_, err := s.db.Exec(`INSERT INTO local_deliveries (message_id, side, status, error, at) VALUES (?,?,?,?,?)
		ON CONFLICT(message_id, side) DO UPDATE SET status=excluded.status, error=excluded.error, at=excluded.at`, messageID, side, status, errText, now())
	return err
}

func (s *Store) Delivery(messageID, side string) (status, errText, at string, err error) {
	err = s.db.QueryRow(`SELECT status, error, at FROM local_deliveries WHERE message_id=? AND side=?`, messageID, side).Scan(&status, &errText, &at)
	return
}

func (s *Store) LastDelivery(channelID int64, side string) (messageID, status, errText, at string, err error) {
	err = s.db.QueryRow(`SELECT d.message_id, d.status, d.error, d.at FROM local_deliveries d JOIN messages m ON m.id=d.message_id
		WHERE m.channel_id=? AND d.side=? ORDER BY m.created_at DESC, m.seq DESC LIMIT 1`, channelID, side).Scan(&messageID, &status, &errText, &at)
	return
}

func (s *Store) listMessagesWhere(where string, args ...any) ([]Message, error) {
	rows, err := s.db.Query(`SELECT m.id, m.channel_id, m.sender_id, u.username, u.display_name, u.avatar, m.summary, m.body_md, COALESCE(m.in_reply_to,''), m.created_at,
		m.seq, m.kind, m.owner, m.owner_reason, m.ack_of FROM messages m JOIN users u ON u.id=m.sender_id WHERE `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var created string
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.SenderID, &m.Sender, &m.SenderDisplay, &m.SenderAvatar, &m.Summary, &m.Body, &m.InReplyTo, &created,
			&m.Seq, &m.Kind, &m.Owner, &m.OwnerReason, &m.AckOf); err != nil {
			return nil, err
		}
		m.CreatedAt = parseTime(created)
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		to, err := s.recipientNames(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].To = to
	}
	return out, nil
}

func (s *Store) ListAfterSeq(channelID int64, seq int) ([]Message, error) {
	return s.listMessagesWhere(`m.channel_id=? AND m.seq>? ORDER BY m.seq`, channelID, seq)
}

func (s *Store) ListKickoffs(channelID int64) ([]Message, error) {
	return s.listMessagesWhere(`m.channel_id=? AND m.kind='kickoff' ORDER BY m.created_at, m.id`, channelID)
}

func (s *Store) SeqOf(messageID string) (int, error) {
	var seq int
	err := s.db.QueryRow(`SELECT seq FROM messages WHERE id=?`, messageID).Scan(&seq)
	return seq, err
}

func (s *Store) MessageIDBySeq(channelID int64, seq int) (string, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM messages WHERE channel_id=? AND seq=?`, channelID, seq).Scan(&id)
	return id, err
}

// SentKeyExists：outbox 幂等键是否已经落过库；供守卫判断这次入库是不是重放
// （文件因为后续步骤失败而没被删掉，下一轮重新入库同一个 IdemKey），M9 Task 7 修复轮 1 F2。
func (s *Store) SentKeyExists(channelID int64, key string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM sent_keys WHERE channel_id=? AND key=?`, channelID, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// CountLocalTurn：本地频道每入库一封 agent 信记一条；到上限置 needs-human（沿用 capHitQuestion 文案）。不拒收。
func (s *Store) CountLocalTurn(channelID int64) (bool, error) {
	if _, err := s.db.Exec(`INSERT INTO channel_auto (channel_id, enabled, round_count, cap, paused, needs_human_q) VALUES (?,1,0,16,0,'')
		ON CONFLICT(channel_id) DO NOTHING`, channelID); err != nil {
		return false, err
	}
	if _, err := s.db.Exec(`UPDATE channel_auto SET round_count=round_count+1 WHERE channel_id=?`, channelID); err != nil {
		return false, err
	}
	var round, cap int
	var q string
	if err := s.db.QueryRow(`SELECT round_count, cap, needs_human_q FROM channel_auto WHERE channel_id=?`, channelID).Scan(&round, &cap, &q); err != nil {
		return false, err
	}
	if round < cap {
		return false, nil
	}
	if q == "" {
		if err := s.SetNeedsHuman(channelID, s.capHitQuestion(channelID, cap)); err != nil {
			return true, err
		}
	}
	return true, nil
}

// SetCap 调整频道自动回合上限，不重置已有的 round_count，也不改变 enabled（不同于 SetAutoEnabled）。
func (s *Store) SetCap(channelID int64, cap int) error {
	_, err := s.db.Exec(`INSERT INTO channel_auto (channel_id, cap) VALUES (?,?)
		ON CONFLICT(channel_id) DO UPDATE SET cap=excluded.cap`, channelID, cap)
	return err
}

// MaxSeq 返回频道内最大 seq；空频道为 0。
func (s *Store) MaxSeq(channelID int64) (int, error) {
	var seq int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM messages WHERE channel_id=?`, channelID).Scan(&seq)
	return seq, err
}

func (s *Store) RenameChannel(channelID int64, newName string) error {
	_, err := s.db.Exec(`UPDATE channels SET name=? WHERE id=?`, newName, channelID)
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return ErrChannelExists
	}
	return err
}

func (s *Store) DeleteChannel(channelID int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM attachments WHERE message_id IN (SELECT id FROM messages WHERE channel_id=?)`,
		`DELETE FROM local_deliveries WHERE message_id IN (SELECT id FROM messages WHERE channel_id=?)`,
		`DELETE FROM recipients WHERE message_id IN (SELECT id FROM messages WHERE channel_id=?)`,
		`DELETE FROM sent_keys WHERE channel_id=?`,
		`DELETE FROM drafts WHERE channel_id=?`,
		`DELETE FROM messages WHERE channel_id=?`,
		`DELETE FROM channel_auto WHERE channel_id=?`,
		`DELETE FROM guidance WHERE channel_id=?`,
		`DELETE FROM invites WHERE channel_id=?`,
		`DELETE FROM members WHERE channel_id=?`,
		`DELETE FROM local_modules WHERE channel_id=?`,
		`DELETE FROM channels WHERE id=?`,
	} {
		if _, err := tx.Exec(q, channelID); err != nil {
			return fmt.Errorf("删除频道: %s: %w", q, err)
		}
	}
	return tx.Commit()
}

func (s *Store) ReopenChannel(channelID int64) error {
	_, err := s.db.Exec(`INSERT INTO channel_auto (channel_id, closed, paused) VALUES (?,0,0)
		ON CONFLICT(channel_id) DO UPDATE SET closed=0, paused=0`, channelID)
	return err
}

// PendingCodexDeliveries：该投给 codex 却还没投成功的消息 id（终审修复 #2）。
// 条件：收件人含 codex 账号或是 kickoff；已归档（普通信 seq<=archived_seq，kickoff 有
// archive 投递记录）；不是 codex 自己发的；没有 (id,'codex',status='ok') 的投递记录。
// 按信的先后排列：普通信按 seq；kickoff 不占 seq（seq=0）、常与结论同一秒建，所以按它
// ack_of 指向的结论的 seq 排、并排在该结论之后（终审修复补）。
func (s *Store) PendingCodexDeliveries(channelID int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT m.id FROM messages m
		JOIN local_modules lm ON lm.channel_id=m.channel_id
		JOIN users su ON su.id=m.sender_id
		WHERE m.channel_id=? AND su.username<>'codex'
		AND (m.kind='kickoff' OR EXISTS (SELECT 1 FROM recipients r JOIN users u ON u.id=r.user_id WHERE r.message_id=m.id AND u.username='codex'))
		AND ((m.kind<>'kickoff' AND m.seq>0 AND m.seq<=lm.archived_seq)
		     OR (m.kind='kickoff' AND EXISTS (SELECT 1 FROM local_deliveries d WHERE d.message_id=m.id AND d.side='archive')))
		AND NOT EXISTS (SELECT 1 FROM local_deliveries d WHERE d.message_id=m.id AND d.side='codex' AND d.status='ok')
		ORDER BY COALESCE((SELECT a.seq FROM messages a WHERE a.id=m.ack_of AND m.kind='kickoff'), m.seq), (m.kind='kickoff'), m.created_at, m.id`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// MarkChannelDelivered：把频道里现有的消息都记为「已归档、已投两侧」（只补没有记录的）。
// 接管一个已有历史的频道（M8 升级迁移、CreateModule 收编已有频道）时用：旧信与旧 kickoff
// 不重新归档、不补投（终审修复 #3）。
func (s *Store) MarkChannelDelivered(channelID int64) error {
	at := now()
	for _, q := range []string{
		`INSERT INTO local_deliveries (message_id, side, status, error, at)
			SELECT id, 'archive', 'ok', '', ? FROM messages WHERE channel_id=? AND kind='kickoff'
			ON CONFLICT(message_id, side) DO NOTHING`,
		`INSERT INTO local_deliveries (message_id, side, status, error, at)
			SELECT id, 'claude', 'ok', '', ? FROM messages WHERE channel_id=?
			ON CONFLICT(message_id, side) DO NOTHING`,
		`INSERT INTO local_deliveries (message_id, side, status, error, at)
			SELECT id, 'codex', 'ok', '', ? FROM messages WHERE channel_id=?
			ON CONFLICT(message_id, side) DO NOTHING`,
	} {
		if _, err := s.db.Exec(q, at, channelID); err != nil {
			return err
		}
	}
	return nil
}
