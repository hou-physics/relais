// local.go 支持"接入现有对话"（M9）：本地模块登记表、投递记录、
// 频道改名/删除/重开，以及本地回合计数。
package store

import (
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

func (s *Store) SetCodexAttach(channelID int64, threadID, threadName, at string) error {
	_, err := s.db.Exec(`UPDATE local_modules SET codex_thread_id=?, codex_thread_name=?, codex_attached_at=?, codex_delivery_error='' WHERE channel_id=?`, threadID, threadName, at, channelID)
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
