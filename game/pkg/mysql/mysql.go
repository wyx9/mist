package mysql

import (
	"fmt"

	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"

	"simple_game/game/pkg/logger"
)

var DB *sqlx.DB

func MysqlStart(dsn string) {
	database, err := sqlx.Open("mysql", dsn)
	if err != nil || database == nil {
		fmt.Println("mysql conn - fail-", err)
		return
	} else {
		logger.INFO("mysql server start suc")
	}
	DB = database
}
