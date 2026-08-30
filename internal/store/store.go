// Package store 是 Relais 的唯一事实源：SQLite 存储与全部查询。
// 双钥匙可见性（spec §5）在本包的查询层强制，handler 只做身份解析。
package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	err := row.Scan(&u.ID, &u.Username, &u.DisplayName, &hash, &u.AgentToken, &u.Avatar)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", ErrNotFound
	}
	if err != nil {
		return nil, "", err
	}
	return &u, hash, nil
}

const userCols = `id, username, display_name, password_hash, agent_token, avatar`

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
}

func (s *Store) SaveMessage(channelID, senderID int64, toIDs []int64, summary, body, inReplyTo string) (*Message, error) {
	id := ulid.Make().String()
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	createdAt := now()
	var replyVal any
	if inReplyTo != "" {
		replyVal = inReplyTo
	}
	if _, err := tx.Exec(`INSERT INTO messages (id, channel_id, sender_id, summary, body_md, in_reply_to, created_at)
		VALUES (?,?,?,?,?,?,?)`, id, channelID, senderID, summary, body, replyVal, createdAt); err != nil {
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
			&m.Summary, &m.InReplyTo, &created, &unread); err != nil {
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
		m.summary, m.body_md, COALESCE(m.in_reply_to,''), m.created_at
		FROM messages m JOIN users u ON u.id=m.sender_id WHERE m.id=?`, id).
		Scan(&m.ID, &m.ChannelID, &m.SenderID, &m.Sender, &m.SenderDisplay, &m.SenderAvatar,
			&m.Summary, &m.Body, &m.InReplyTo, &created)
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
