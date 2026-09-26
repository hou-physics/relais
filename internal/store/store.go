// Package store 是 Relais 的唯一事实源：SQLite 存储与全部查询。
// 双钥匙可见性（spec §5）在本包的查询层强制，handler 只做身份解析。
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

var (
	ErrNotFound  = errors.New("不存在")
	ErrAuth      = errors.New("用户名或密码错误")
	ErrForbidden = errors.New("无权访问")
)

const schema = `
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  display_name TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  agent_token TEXT NOT NULL UNIQUE,
  avatar TEXT NOT NULL DEFAULT '',
  is_admin INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS channels (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL UNIQUE,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS members (
  channel_id INTEGER NOT NULL REFERENCES channels(id),
  user_id INTEGER NOT NULL REFERENCES users(id),
  joined_at TEXT NOT NULL,
  PRIMARY KEY (channel_id, user_id)
);
CREATE TABLE IF NOT EXISTS messages (
  id TEXT PRIMARY KEY,
  channel_id INTEGER NOT NULL REFERENCES channels(id),
  sender_id INTEGER NOT NULL REFERENCES users(id),
  summary TEXT NOT NULL,
  body_md TEXT NOT NULL,
  in_reply_to TEXT,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS recipients (
  message_id TEXT NOT NULL REFERENCES messages(id),
  user_id INTEGER NOT NULL REFERENCES users(id),
  read_at TEXT,
  PRIMARY KEY (message_id, user_id)
);
CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id),
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS invites (
  code TEXT PRIMARY KEY,
  channel_id INTEGER REFERENCES channels(id),
  created_by INTEGER NOT NULL REFERENCES users(id),
  expires_at TEXT NOT NULL,
  used_at TEXT
);
-- attachments 表 M2 才使用（spec §4 数据模型完整性），M1 仅建表
CREATE TABLE IF NOT EXISTS attachments (
  id TEXT PRIMARY KEY,
  message_id TEXT NOT NULL REFERENCES messages(id),
  filename TEXT NOT NULL,
  stored_path TEXT NOT NULL,
  size INTEGER NOT NULL,
  mime TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS drafts (
  id TEXT PRIMARY KEY,
  channel_id INTEGER NOT NULL REFERENCES channels(id),
  author_id INTEGER NOT NULL REFERENCES users(id),
  to_json TEXT NOT NULL,
  summary TEXT NOT NULL,
  body_md TEXT NOT NULL,
  in_reply_to TEXT,
  created_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS channel_auto (
  channel_id INTEGER PRIMARY KEY REFERENCES channels(id),
  enabled INTEGER NOT NULL DEFAULT 0,
  round_count INTEGER NOT NULL DEFAULT 0,
  cap INTEGER NOT NULL DEFAULT 6,
  paused INTEGER NOT NULL DEFAULT 0,
  needs_human_q TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS guidance (
  channel_id INTEGER NOT NULL REFERENCES channels(id),
  user_id INTEGER NOT NULL REFERENCES users(id),
  note TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (channel_id, user_id)
);
CREATE TABLE IF NOT EXISTS sent_keys (
  channel_id INTEGER NOT NULL REFERENCES channels(id),
  key TEXT NOT NULL,
  message_id TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (channel_id, key)
);
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
`

type Store struct{ db *sql.DB }

func Open(path string) (*Store, error) {
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN avatar TEXT NOT NULL DEFAULT ''`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return nil, err
	}
	if _, err := db.Exec(`ALTER TABLE users ADD COLUMN is_admin INTEGER NOT NULL DEFAULT 0`); err != nil &&
		!strings.Contains(err.Error(), "duplicate column") {
		return nil, err
	}
	for _, ddl := range []string{
		`ALTER TABLE messages ADD COLUMN seq INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN owner TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN owner_reason TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE messages ADD COLUMN ack_of TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE channel_auto ADD COLUMN resolved INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE channel_auto ADD COLUMN resolution_msg_id TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE channel_auto ADD COLUMN mode TEXT NOT NULL DEFAULT 'supervised'`,
		`ALTER TABLE channel_auto ADD COLUMN kicked_off INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE channel_auto ADD COLUMN closed INTEGER NOT NULL DEFAULT 0`,
	} {
		if _, err := db.Exec(ddl); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return nil, err
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().UTC().Format(time.RFC3339) }

func randomToken(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

type User struct {
	ID          int64
	Username    string
	DisplayName string
	AgentToken  string
	Avatar      string
	IsAdmin     bool
}

func (s *Store) CreateUser(username, displayName, password string) (*User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	token := randomToken(24)
	res, err := s.db.Exec(
		`INSERT INTO users (username, display_name, password_hash, agent_token, created_at) VALUES (?,?,?,?,?)`,
		username, displayName, string(hash), token, now())
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Username: username, DisplayName: displayName, AgentToken: token, Avatar: ""}, nil
}

func (s *Store) scanUser(row *sql.Row) (*User, string, error) {
	var u User
	var hash string
	var adminInt int
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &hash, &u.AgentToken, &u.Avatar, &adminInt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	u.IsAdmin = adminInt == 1
	return &u, hash, nil
}

const userCols = `id, username, display_name, password_hash, agent_token, avatar, is_admin`

func (s *Store) UserByName(username string) (*User, error) {
	u, _, err := s.scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username=?`, username))
	return u, err
}

func (s *Store) UserByAgentToken(token string) (*User, error) {
	u, _, err := s.scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE agent_token=?`, token))
	return u, err
}

func (s *Store) Authenticate(username, password string) (*User, error) {
	u, hash, err := s.scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username=?`, username))
	if errors.Is(err, ErrNotFound) {
		return nil, ErrAuth
	}
	if err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return nil, ErrAuth
	}
	return u, nil
}

type Channel struct {
	ID   int64
	Name string
}

func (s *Store) CreateChannel(name string) (*Channel, error) {
	res, err := s.db.Exec(`INSERT INTO channels (name, created_at) VALUES (?,?)`, name, now())
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &Channel{ID: id, Name: name}, nil
}

func (s *Store) ChannelByName(name string) (*Channel, error) {
	var c Channel
	err := s.db.QueryRow(`SELECT id, name FROM channels WHERE name=?`, name).Scan(&c.ID, &c.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) AddMember(channelID, userID int64) error {
	_, err := s.db.Exec(`INSERT OR IGNORE INTO members (channel_id, user_id, joined_at) VALUES (?,?,?)`,
		channelID, userID, now())
	return err
}

func (s *Store) ListMembers(channelID int64) ([]User, error) {
	rows, err := s.db.Query(`SELECT u.id, u.username, u.display_name, u.agent_token, u.avatar
		FROM members m JOIN users u ON u.id=m.user_id WHERE m.channel_id=? ORDER BY u.username`, channelID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		var u User
		if err := rows.Scan(&u.ID, &u.Username, &u.DisplayName, &u.AgentToken, &u.Avatar); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) IsMember(channelID, userID int64) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM members WHERE channel_id=? AND user_id=?`, channelID, userID).Scan(&n)
	return n > 0, err
}

type Message struct {
	ID            string
	ChannelID     int64
	SenderID      int64
	Sender        string
	SenderDisplay string
	SenderAvatar  string
	To            []string
	Summary       string
	Body          string
	InReplyTo     string
	CreatedAt     time.Time
	Unread        bool
	Seq           int
	Kind          string // "" | resolved | conclusion | kickoff
	Owner         string // claude | codex | user（kind 非空时）
	OwnerReason   string
	AckOf         string
}

// Round 把频道序号换算成回合（一来一回 = 1）。
func Round(seq int) int {
	if seq <= 0 {
		return 0
	}
	return (seq + 1) / 2
}

type SaveOpts struct {
	Kind, Owner, OwnerReason, AckOf string
	IdemKey                         string // 非空则幂等：同频道同 key 只落一条
}

func (s *Store) SaveMessage(channelID, senderID int64, toIDs []int64, summary, body, inReplyTo string) (*Message, error) {
	return s.SaveMessageOpts(channelID, senderID, toIDs, summary, body, inReplyTo, SaveOpts{})
}

func (s *Store) SaveMessageOpts(channelID, senderID int64, toIDs []int64, summary, body, inReplyTo string, o SaveOpts) (*Message, error) {
	id := ulid.Make().String()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if o.IdemKey != "" {
		var existing string
		err := tx.QueryRow(`SELECT message_id FROM sent_keys WHERE channel_id=? AND key=?`, channelID, o.IdemKey).Scan(&existing)
		if err == nil {
			tx.Rollback()
			return s.GetMessage(existing, senderID, true)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if _, err := tx.Exec(`INSERT INTO sent_keys (channel_id, key, message_id, created_at) VALUES (?,?,?,?)`,
			channelID, o.IdemKey, id, now()); err != nil {
			// 并发下另一事务先插入了同 key：让它赢
			tx.Rollback()
			var winner string
			if e2 := s.db.QueryRow(`SELECT message_id FROM sent_keys WHERE channel_id=? AND key=?`, channelID, o.IdemKey).Scan(&winner); e2 == nil {
				return s.GetMessage(winner, senderID, true)
			}
			return nil, err
		}
	}
	createdAt := now()
	var replyVal any
	if inReplyTo != "" {
		replyVal = inReplyTo
	}
	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0)+1 FROM messages WHERE channel_id=?`, channelID).Scan(&seq); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`INSERT INTO messages (id, channel_id, sender_id, summary, body_md, in_reply_to, created_at, seq, kind, owner, owner_reason, ack_of)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`, id, channelID, senderID, summary, body, replyVal, createdAt, seq, o.Kind, o.Owner, o.OwnerReason, o.AckOf); err != nil {
		return nil, err
	}
	for _, uid := range toIDs {
		if _, err := tx.Exec(`INSERT INTO recipients (message_id, user_id) VALUES (?,?)`, id, uid); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetMessage(id, senderID, true)
}

func (s *Store) recipientNames(messageID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT u.username FROM recipients r JOIN users u ON u.id=r.user_id
		WHERE r.message_id=? ORDER BY u.username`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		names = append(names, n)
	}
	return names, rows.Err()
}

const envelopeQuery = `
SELECT m.id, m.channel_id, m.sender_id, u.username, u.display_name, u.avatar,
       m.summary, COALESCE(m.in_reply_to,''), m.created_at,
       m.seq, m.kind, m.owner, m.owner_reason, m.ack_of,
       EXISTS(SELECT 1 FROM recipients ru WHERE ru.message_id=m.id AND ru.user_id=?1 AND ru.read_at IS NULL)
FROM messages m JOIN users u ON u.id=m.sender_id
WHERE m.channel_id=?2
  AND (?3=0 OR m.sender_id=?1 OR EXISTS(SELECT 1 FROM recipients r WHERE r.message_id=m.id AND r.user_id=?1))
  AND (?4=0 OR EXISTS(SELECT 1 FROM recipients r2 WHERE r2.message_id=m.id AND r2.user_id=?1 AND r2.read_at IS NULL))
ORDER BY m.id`

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (s *Store) ListEnvelopes(channelID, viewerID int64, agentKey, unreadOnly bool) ([]Message, error) {
	ok, err := s.IsMember(channelID, viewerID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrForbidden
	}
	rows, err := s.db.Query(envelopeQuery, viewerID, channelID, b2i(agentKey), b2i(unreadOnly))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var created string
		var unread int
		if err := rows.Scan(&m.ID, &m.ChannelID, &m.SenderID, &m.Sender, &m.SenderDisplay, &m.SenderAvatar,
			&m.Summary, &m.InReplyTo, &created,
			&m.Seq, &m.Kind, &m.Owner, &m.OwnerReason, &m.AckOf, &unread); err != nil {
			return nil, err
		}
		m.CreatedAt, _ = time.Parse(time.RFC3339, created)
		m.Unread = unread == 1
		if m.To, err = s.recipientNames(m.ID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GetMessage(id string, viewerID int64, agentKey bool) (*Message, error) {
	var m Message
	var created string
	err := s.db.QueryRow(`SELECT m.id, m.channel_id, m.sender_id, u.username, u.display_name, u.avatar,
		m.summary, m.body_md, COALESCE(m.in_reply_to,''), m.created_at,
		m.seq, m.kind, m.owner, m.owner_reason, m.ack_of
		FROM messages m JOIN users u ON u.id=m.sender_id WHERE m.id=?`, id).
		Scan(&m.ID, &m.ChannelID, &m.SenderID, &m.Sender, &m.SenderDisplay, &m.SenderAvatar,
			&m.Summary, &m.Body, &m.InReplyTo, &created,
			&m.Seq, &m.Kind, &m.Owner, &m.OwnerReason, &m.AckOf)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	m.CreatedAt, _ = time.Parse(time.RFC3339, created)
	if m.To, err = s.recipientNames(m.ID); err != nil {
		return nil, err
	}
	viewer, err := s.userByID(viewerID)
	if err != nil {
		return nil, err
	}
	if agentKey {
		// 核心不变量（spec §5）：agent 只能读自己主人为发件人或收件人的消息
		if m.SenderID != viewerID && !contains(m.To, viewer.Username) {
			return nil, ErrForbidden
		}
	} else {
		ok, err := s.IsMember(m.ChannelID, viewerID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrForbidden
		}
	}
	var unread int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM recipients WHERE message_id=? AND user_id=? AND read_at IS NULL`,
		id, viewerID).Scan(&unread); err != nil {
		return nil, err
	}
	m.Unread = unread == 1
	return &m, nil
}

func (s *Store) userByID(id int64) (*User, error) {
	u, _, err := s.scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id=?`, id))
	return u, err
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Store) MarkRead(messageID string, userID int64) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM recipients WHERE message_id=? AND user_id=?`,
		messageID, userID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrForbidden
	}
	_, err := s.db.Exec(`UPDATE recipients SET read_at=? WHERE message_id=? AND user_id=? AND read_at IS NULL`,
		now(), messageID, userID)
	return err
}

type ChannelInfo struct {
	Name   string
	Unread int
}

func (s *Store) ChannelsForUser(userID int64) ([]ChannelInfo, error) {
	rows, err := s.db.Query(`SELECT c.name,
		(SELECT COUNT(*) FROM recipients r JOIN messages m2 ON m2.id=r.message_id
		 WHERE m2.channel_id=c.id AND r.user_id=?1 AND r.read_at IS NULL)
		FROM channels c JOIN members mb ON mb.channel_id=c.id AND mb.user_id=?1 ORDER BY c.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelInfo
	for rows.Next() {
		var ci ChannelInfo
		if err := rows.Scan(&ci.Name, &ci.Unread); err != nil {
			return nil, err
		}
		out = append(out, ci)
	}
	return out, rows.Err()
}

func (s *Store) CreateSession(userID int64) (string, error) {
	token := randomToken(24)
	_, err := s.db.Exec(`INSERT INTO sessions (token, user_id, created_at) VALUES (?,?,?)`, token, userID, now())
	return token, err
}

// UserBySession 查找 session 对应的用户，拒绝超过 90 天的老 session（服务端强制过期）。
func (s *Store) UserBySession(token string) (*User, error) {
	cutoff := time.Now().UTC().Add(-90 * 24 * time.Hour).Format(time.RFC3339)
	u, _, err := s.scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users u
		JOIN sessions se ON se.user_id=u.id WHERE se.token=? AND se.created_at > ?`, token, cutoff))
	return u, err
}

func (s *Store) CreateInvite(channelID, createdBy int64, ttl time.Duration) (string, error) {
	code := randomToken(8)
	var chVal any
	if channelID != 0 {
		chVal = channelID
	}
	expires := time.Now().UTC().Add(ttl).Format(time.RFC3339)
	_, err := s.db.Exec(`INSERT INTO invites (code, channel_id, created_by, expires_at) VALUES (?,?,?,?)`,
		code, chVal, createdBy, expires)
	return code, err
}

func (s *Store) inviteRow(code string) (channelID int64, err error) {
	var chID sql.NullInt64
	var expires string
	var used sql.NullString
	err = s.db.QueryRow(`SELECT channel_id, expires_at, used_at FROM invites WHERE code=?`, code).
		Scan(&chID, &expires, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	exp, perr := time.Parse(time.RFC3339, expires)
	if perr != nil || used.Valid || time.Now().UTC().After(exp) {
		return 0, ErrNotFound
	}
	return chID.Int64, nil
}

func (s *Store) InviteChannel(code string) (string, error) {
	chID, err := s.inviteRow(code)
	if err != nil {
		return "", err
	}
	if chID == 0 {
		return "", nil
	}
	var name string
	if err := s.db.QueryRow(`SELECT name FROM channels WHERE id=?`, chID).Scan(&name); err != nil {
		return "", err
	}
	return name, nil
}

func (s *Store) ConsumeInvite(code string) (int64, error) {
	chID, err := s.inviteRow(code)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`UPDATE invites SET used_at=? WHERE code=? AND used_at IS NULL`, now(), code)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n == 0 {
		return 0, ErrNotFound
	}
	return chID, nil
}

func (s *Store) ChannelNameByID(id int64) (string, error) {
	var name string
	err := s.db.QueryRow(`SELECT name FROM channels WHERE id=?`, id).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

// FirstUser 返回 id 最小的用户（服务器本机 invite 命令的记账主体）。
func (s *Store) FirstUser() (*User, error) {
	u, _, err := s.scanUser(s.db.QueryRow(`SELECT ` + userCols + ` FROM users ORDER BY id LIMIT 1`))
	return u, err
}

type Draft struct {
	ID        string
	ChannelID int64
	AuthorID  int64
	To        []string
	Summary   string
	Body      string
	InReplyTo string
	CreatedAt time.Time
}

func (s *Store) CreateDraft(channelID, authorID int64, to []string, summary, body, inReplyTo string) (*Draft, error) {
	id := ulid.Make().String()
	toJSON, err := json.Marshal(to)
	if err != nil {
		return nil, err
	}
	var replyVal any
	if inReplyTo != "" {
		replyVal = inReplyTo
	}
	createdAt := now()
	if _, err := s.db.Exec(`INSERT INTO drafts (id, channel_id, author_id, to_json, summary, body_md, in_reply_to, created_at)
		VALUES (?,?,?,?,?,?,?,?)`, id, channelID, authorID, string(toJSON), summary, body, replyVal, createdAt); err != nil {
		return nil, err
	}
	return s.GetDraft(id, authorID)
}

func (s *Store) scanDraft(row interface{ Scan(...any) error }) (*Draft, error) {
	var d Draft
	var toJSON, created string
	err := row.Scan(&d.ID, &d.ChannelID, &d.AuthorID, &toJSON, &d.Summary, &d.Body, &d.InReplyTo, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(toJSON), &d.To); err != nil {
		return nil, err
	}
	d.CreatedAt, _ = time.Parse(time.RFC3339, created)
	return &d, nil
}

const draftCols = `id, channel_id, author_id, to_json, summary, body_md, COALESCE(in_reply_to,''), created_at`

func (s *Store) GetDraft(id string, authorID int64) (*Draft, error) {
	return s.scanDraft(s.db.QueryRow(`SELECT `+draftCols+` FROM drafts WHERE id=? AND author_id=?`, id, authorID))
}

func (s *Store) ListDrafts(channelID, authorID int64) ([]Draft, error) {
	rows, err := s.db.Query(`SELECT `+draftCols+` FROM drafts WHERE channel_id=? AND author_id=? ORDER BY id`, channelID, authorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Draft
	for rows.Next() {
		d, err := s.scanDraft(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (s *Store) DeleteDraft(id string, authorID int64) error {
	res, err := s.db.Exec(`DELETE FROM drafts WHERE id=? AND author_id=?`, id, authorID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) UpdatePassword(userID int64, oldPw, newPw string) error {
	var hash string
	err := s.db.QueryRow(`SELECT password_hash FROM users WHERE id=?`, userID).Scan(&hash)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(oldPw)) != nil {
		return ErrAuth
	}
	newHash, err := bcrypt.GenerateFromPassword([]byte(newPw), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE users SET password_hash=? WHERE id=?`, string(newHash), userID)
	return err
}

func (s *Store) RegenerateToken(userID int64) (string, error) {
	token := randomToken(24)
	_, err := s.db.Exec(`UPDATE users SET agent_token=? WHERE id=?`, token, userID)
	return token, err
}

func (s *Store) UpdateProfile(userID int64, displayName, avatar string) error {
	_, err := s.db.Exec(`UPDATE users SET display_name=?, avatar=? WHERE id=?`, displayName, avatar, userID)
	return err
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token=?`, token)
	return err
}

func (s *Store) SetAdmin(userID int64, admin bool) error {
	v := 0
	if admin {
		v = 1
	}
	_, err := s.db.Exec(`UPDATE users SET is_admin=? WHERE id=?`, v, userID)
	return err
}

type ChannelStat struct {
	Name    string
	Members int
}

func (s *Store) AllChannels() ([]ChannelStat, error) {
	rows, err := s.db.Query(`SELECT c.name, (SELECT COUNT(*) FROM members m WHERE m.channel_id=c.id)
		FROM channels c ORDER BY c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChannelStat
	for rows.Next() {
		var st ChannelStat
		if err := rows.Scan(&st.Name, &st.Members); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

func (s *Store) RemoveMember(channelID, userID int64) error {
	res, err := s.db.Exec(`DELETE FROM members WHERE channel_id=? AND user_id=?`, channelID, userID)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

type AutoState struct {
	Enabled         bool
	RoundCount      int
	Cap             int
	Paused          bool
	NeedsHumanQ     string
	Mode            string // supervised | autopilot
	Resolved        bool
	ResolutionMsgID string
	KickedOff       bool
	Closed          bool
	InFlight        bool // 最新一条（按 seq）仍有收件人未读
}

func (s *Store) GetAuto(channelID int64) (AutoState, error) {
	a := AutoState{Enabled: false, Cap: 6, Mode: "supervised"}
	var en, paused, resolved, kicked, closed int
	err := s.db.QueryRow(`SELECT enabled, round_count, cap, paused, needs_human_q, mode, resolved, resolution_msg_id, kicked_off, closed
		FROM channel_auto WHERE channel_id=?`, channelID).
		Scan(&en, &a.RoundCount, &a.Cap, &paused, &a.NeedsHumanQ, &a.Mode, &resolved, &a.ResolutionMsgID, &kicked, &closed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return a, err
	}
	if err == nil {
		a.Enabled, a.Paused, a.Resolved, a.KickedOff, a.Closed = en == 1, paused == 1, resolved == 1, kicked == 1, closed == 1
	}
	var unread int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM recipients r WHERE r.read_at IS NULL AND r.message_id =
		(SELECT id FROM messages WHERE channel_id=? AND seq>0 ORDER BY seq DESC LIMIT 1)`, channelID).Scan(&unread); err != nil {
		return a, err
	}
	a.InFlight = unread > 0
	return a, nil
}

func (s *Store) SetAutoEnabled(channelID int64, enabled bool, cap int) error {
	en := 0
	if enabled {
		en = 1
	}
	if cap < 1 {
		cap = 6
	}
	_, err := s.db.Exec(`INSERT INTO channel_auto (channel_id, enabled, round_count, cap, paused, needs_human_q)
		VALUES (?,?,0,?,0,'')
		ON CONFLICT(channel_id) DO UPDATE SET enabled=excluded.enabled, cap=excluded.cap, round_count=0, paused=0, needs_human_q=''`,
		channelID, en, cap)
	return err
}

func (s *Store) RequestTurn(channelID int64) (bool, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, "", err
	}
	defer tx.Rollback()
	var en, paused, round, cap, closed, resolved int
	var q string
	err = tx.QueryRow(`SELECT enabled, paused, round_count, cap, needs_human_q, closed, resolved FROM channel_auto WHERE channel_id=?`, channelID).
		Scan(&en, &paused, &round, &cap, &q, &closed, &resolved)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "自动模式未开启", nil
	}
	if err != nil {
		return false, "", err
	}
	if closed == 1 {
		return false, "频道已关闭", nil
	}
	if resolved == 1 {
		return false, "已握手，等人处理", nil
	}
	if en != 1 {
		return false, "自动模式未开启", nil
	}
	if paused == 1 || q != "" {
		return false, "已暂停（等待人处理）", nil
	}
	if round >= cap {
		tx.Rollback()
		q := s.capHitQuestion(channelID, cap)
		if err := s.SetNeedsHuman(channelID, q); err != nil {
			return false, "", err
		}
		return false, "已到回合上限（等待人决定）", nil
	}
	res, err := tx.Exec(`UPDATE channel_auto SET round_count=round_count+1 WHERE channel_id=? AND round_count<cap`, channelID)
	if err != nil {
		return false, "", err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, "", err
	}
	if n == 0 {
		// 竞态下另一并发请求已用掉最后一个名额
		tx.Rollback()
		q := s.capHitQuestion(channelID, cap)
		if err := s.SetNeedsHuman(channelID, q); err != nil {
			return false, "", err
		}
		return false, "已到回合上限（等待人决定）", nil
	}
	if err := tx.Commit(); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// capHitQuestion 组装"回合上限已到"的 needs-human 文案，附最近两位发言者各自最后一句摘要。
func (s *Store) capHitQuestion(channelID int64, cap int) string {
	rows, err := s.db.Query(`SELECT u.username, m.summary FROM messages m JOIN users u ON u.id=m.sender_id
		WHERE m.channel_id=? AND m.seq>0
		  AND m.seq = (SELECT MAX(seq) FROM messages WHERE channel_id=m.channel_id AND sender_id=m.sender_id)
		ORDER BY m.seq DESC LIMIT 2`, channelID)
	positions := ""
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var who, sum string
			if rows.Scan(&who, &sum) == nil {
				positions += who + "：" + sum + " / "
			}
		}
	}
	return fmt.Sprintf("回合上限已到（%d 回合）。最后立场：%s再放几轮、你来裁、还是关掉？", Round(cap), positions)
}

func (s *Store) PauseAuto(channelID int64) error {
	_, err := s.db.Exec(`UPDATE channel_auto SET paused=1 WHERE channel_id=?`, channelID)
	return err
}

func (s *Store) ResumeAuto(channelID int64) error {
	_, err := s.db.Exec(`UPDATE channel_auto SET paused=0, round_count=0, needs_human_q='' WHERE channel_id=?`, channelID)
	return err
}

func (s *Store) SetNeedsHuman(channelID int64, q string) error {
	_, err := s.db.Exec(`UPDATE channel_auto SET paused=1, needs_human_q=? WHERE channel_id=?`, q, channelID)
	return err
}

type HandshakeResult int

const (
	HandshakeNone          HandshakeResult = iota // 不构成握手（首次提议 / ack 无效）
	HandshakeDone                                 // 握手成立：resolved=1, paused=1, 第二条改 conclusion
	HandshakeOwnerConflict                        // 前三条件成立但 owner 不同 → needs-human
)

// EvaluateHandshake 在 m2（kind=resolved）入库后判定是否与它 ack_of 指向的提议构成握手（spec §6.2）。
func (s *Store) EvaluateHandshake(channelID int64, m2ID string) (HandshakeResult, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return HandshakeNone, err
	}
	defer tx.Rollback()
	var m2Sender int64
	var m2Kind, m2Owner, m2Reason, ackOf string
	var m2Seq int
	if err := tx.QueryRow(`SELECT sender_id, kind, owner, owner_reason, ack_of, seq FROM messages WHERE id=? AND channel_id=?`, m2ID, channelID).
		Scan(&m2Sender, &m2Kind, &m2Owner, &m2Reason, &ackOf, &m2Seq); err != nil {
		return HandshakeNone, err
	}
	if m2Kind != "resolved" || ackOf == "" {
		return HandshakeNone, nil
	}
	var m1Sender int64
	var m1Kind, m1Owner, m1Reason string
	var m1Seq int
	err = tx.QueryRow(`SELECT sender_id, kind, owner, owner_reason, seq FROM messages WHERE id=? AND channel_id=?`, ackOf, channelID).
		Scan(&m1Sender, &m1Kind, &m1Owner, &m1Reason, &m1Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return HandshakeNone, nil
	}
	if err != nil {
		return HandshakeNone, err
	}
	if m1Kind != "resolved" || m1Sender == m2Sender {
		return HandshakeNone, nil
	}
	var newer int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM messages WHERE channel_id=? AND kind='resolved' AND seq>? AND seq<?`, channelID, m1Seq, m2Seq).Scan(&newer); err != nil {
		return HandshakeNone, err
	}
	if newer > 0 {
		return HandshakeNone, nil
	}
	if m1Owner != m2Owner {
		n1, _ := s.usernameByIDTx(tx, m1Sender)
		n2, _ := s.usernameByIDTx(tx, m2Sender)
		q := fmt.Sprintf("承接方分歧：%s 提名 %s（%s），%s 提名 %s（%s），请定", n1, m1Owner, m1Reason, n2, m2Owner, m2Reason)
		// upsert：频道可能从未 SetAutoEnabled，没有 channel_auto 行，普通 UPDATE 会静默匹配 0 行
		if _, err := tx.Exec(`INSERT INTO channel_auto (channel_id, paused, needs_human_q) VALUES (?,1,?)
			ON CONFLICT(channel_id) DO UPDATE SET paused=1, needs_human_q=excluded.needs_human_q`, channelID, q); err != nil {
			return HandshakeNone, err
		}
		return HandshakeOwnerConflict, tx.Commit()
	}
	if _, err := tx.Exec(`UPDATE messages SET kind='conclusion' WHERE id=?`, m2ID); err != nil {
		return HandshakeNone, err
	}
	// upsert：同上，握手成立时也要在没有 channel_auto 行的频道里把行建出来
	if _, err := tx.Exec(`INSERT INTO channel_auto (channel_id, resolved, resolution_msg_id, paused) VALUES (?,1,?,1)
		ON CONFLICT(channel_id) DO UPDATE SET resolved=1, resolution_msg_id=excluded.resolution_msg_id, paused=1`, channelID, m2ID); err != nil {
		return HandshakeNone, err
	}
	return HandshakeDone, tx.Commit()
}

func (s *Store) usernameByIDTx(tx *sql.Tx, id int64) (string, error) {
	var n string
	err := tx.QueryRow(`SELECT username FROM users WHERE id=?`, id).Scan(&n)
	return n, err
}

var ErrNotResolved = errors.New("频道尚未握手，无结论可开工")

// Kickoff 把当前结论投递为一条 kind=kickoff 消息（发全体成员，不占 seq），并让频道回到空闲。
//
// 并发保护：顶部的 GetAuto 检查只是快速失败的提示，真正的互斥点在事务末尾——
// 状态迁移用 `WHERE resolved=1` 的条件 UPDATE 并检查 RowsAffected，只有仍处于
// resolved=1 的那个调用能把它翻成 0 并提交；另一个并发调用（网页重复点击，或
// 甩手模式下服务器自动调用与人工点击赛跑）会在同一次 UPDATE 里匹配 0 行，
// 从而回滚它已插入但尚未提交的 kickoff 消息，返回 ErrNotResolved。
func (s *Store) Kickoff(channelID, actorID int64) (*Message, error) {
	a, err := s.GetAuto(channelID)
	if err != nil {
		return nil, err
	}
	if !a.Resolved || a.ResolutionMsgID == "" {
		return nil, ErrNotResolved
	}
	var body, owner string
	if err := s.db.QueryRow(`SELECT body_md, owner FROM messages WHERE id=?`, a.ResolutionMsgID).Scan(&body, &owner); err != nil {
		return nil, err
	}
	members, err := s.ListMembers(channelID)
	if err != nil {
		return nil, err
	}
	var to []int64
	for _, m := range members {
		to = append(to, m.ID)
	}
	id := ulid.Make().String()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO messages (id, channel_id, sender_id, summary, body_md, in_reply_to, created_at, seq, kind, owner, owner_reason, ack_of)
		VALUES (?,?,?,?,?,NULL,?,0,'kickoff',?,'',?)`, id, channelID, actorID, "开工 · 承接方 "+owner, body, now(), owner, a.ResolutionMsgID); err != nil {
		return nil, err
	}
	for _, uid := range to {
		if _, err := tx.Exec(`INSERT INTO recipients (message_id, user_id) VALUES (?,?)`, id, uid); err != nil {
			return nil, err
		}
	}
	res, err := tx.Exec(`UPDATE channel_auto SET kicked_off=1, resolved=0, paused=0, round_count=0, needs_human_q='' WHERE channel_id=? AND resolved=1`, channelID)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		// 输给了并发的另一次 Kickoff：defer tx.Rollback() 会丢掉本次已插入的消息
		return nil, ErrNotResolved
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetMessage(id, actorID, false)
}

func (s *Store) Reopen(channelID int64) error {
	_, err := s.db.Exec(`UPDATE channel_auto SET resolved=0, resolution_msg_id='', paused=0, round_count=0, needs_human_q='' WHERE channel_id=?`, channelID)
	return err
}

func (s *Store) SetMode(channelID int64, mode string) error {
	if mode != "supervised" && mode != "autopilot" {
		return fmt.Errorf("mode 只能是 supervised 或 autopilot")
	}
	_, err := s.db.Exec(`INSERT INTO channel_auto (channel_id, mode) VALUES (?,?)
		ON CONFLICT(channel_id) DO UPDATE SET mode=excluded.mode`, channelID, mode)
	return err
}

func (s *Store) CloseChannel(channelID int64) error {
	// upsert：频道可能从未 SetAutoEnabled，没有 channel_auto 行，普通 UPDATE 会静默匹配 0 行
	_, err := s.db.Exec(`INSERT INTO channel_auto (channel_id, closed, paused) VALUES (?,1,1)
		ON CONFLICT(channel_id) DO UPDATE SET closed=1, paused=1`, channelID)
	return err
}

func (s *Store) ClearKickedOff(channelID int64) error {
	_, err := s.db.Exec(`UPDATE channel_auto SET kicked_off=0 WHERE channel_id=?`, channelID)
	return err
}

func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, key, value)
	return err
}

func (s *Store) GetSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

func (s *Store) SetGuidance(channelID, userID int64, note string) error {
	_, err := s.db.Exec(`INSERT INTO guidance (channel_id, user_id, note, created_at) VALUES (?,?,?,?)
		ON CONFLICT(channel_id,user_id) DO UPDATE SET note=excluded.note, created_at=excluded.created_at`,
		channelID, userID, note, now())
	return err
}

func (s *Store) PullGuidance(channelID, userID int64) (string, error) {
	// 单语句 DELETE ... RETURNING：读取并清空原子完成，杜绝 SELECT+DELETE 之间的
	// 并发双读（与 RequestTurn 一样在同一处关掉竞态）。SQLite ≥3.35 支持 RETURNING。
	var note string
	err := s.db.QueryRow(`DELETE FROM guidance WHERE channel_id=? AND user_id=? RETURNING note`, channelID, userID).Scan(&note)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return note, nil
}
