package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"

	"github.com/jmoiron/sqlx"
)

// MysqlStore MySQL 落库接口（抽象以便单测替换）。
type MysqlStore interface {
	EnsureTable(ctx context.Context, table string) error
	Load(ctx context.Context, table string, actorID int64) ([]byte, bool, error)
	Upsert(ctx context.Context, table string, actorID int64, data []byte) error
	Delete(ctx context.Context, table string, actorID int64) error
	Close() error
}

var identRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

// quoteIdent 校验并包裹 MySQL 标识符，防止表名注入。
func quoteIdent(name string) (string, error) {
	if !identRe.MatchString(name) {
		return "", fmt.Errorf("invalid mysql identifier: %q", name)
	}
	return "`" + name + "`", nil
}

type sqlStore struct {
	db     *sqlx.DB
	shared bool // true 表示复用外部连接，Close 为空操作
}

// OpenMysqlStore 打开独立的 MySQL 连接（由 MysqlSync 负责关闭）。
func OpenMysqlStore(dsn string) (MysqlStore, error) {
	db, err := sqlx.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	return &sqlStore{db: db}, nil
}

// WrapMysqlStore 包装已存在的连接，不负责关闭；用于复用全局 mysql.DB。
func WrapMysqlStore(db *sqlx.DB) MysqlStore {
	return &sqlStore{db: db, shared: true}
}

func (s *sqlStore) EnsureTable(ctx context.Context, table string) error {
	ident, err := quoteIdent(table)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s ("+
			"actor_id BIGINT NOT NULL,"+
			"data LONGBLOB NOT NULL,"+
			"version BIGINT NOT NULL DEFAULT 0,"+
			"updated_at DATETIME(3) NOT NULL,"+
			"PRIMARY KEY (actor_id)) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4", ident)
	_, err = s.db.ExecContext(ctx, q)
	return err
}

func (s *sqlStore) Load(ctx context.Context, table string, actorID int64) ([]byte, bool, error) {
	ident, err := quoteIdent(table)
	if err != nil {
		return nil, false, err
	}
	var data []byte
	err = s.db.GetContext(ctx, &data, fmt.Sprintf("SELECT data FROM %s WHERE actor_id = ?", ident), actorID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return data, true, nil
}

func (s *sqlStore) Upsert(ctx context.Context, table string, actorID int64, data []byte) error {
	ident, err := quoteIdent(table)
	if err != nil {
		return err
	}
	q := fmt.Sprintf(
		"INSERT INTO %s (actor_id, data, updated_at) VALUES (?, ?, NOW(3)) "+
			"ON DUPLICATE KEY UPDATE data = VALUES(data), version = version + 1, updated_at = VALUES(updated_at)",
		ident)
	_, err = s.db.ExecContext(ctx, q, actorID, data)
	return err
}

func (s *sqlStore) Delete(ctx context.Context, table string, actorID int64) error {
	ident, err := quoteIdent(table)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE actor_id = ?", ident), actorID)
	return err
}

func (s *sqlStore) Close() error {
	if s.shared {
		return nil
	}
	return s.db.Close()
}
