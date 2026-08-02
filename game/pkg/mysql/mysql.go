package pkg

import (
	"fmt"
	_ "github.com/go-sql-driver/mysql"
	"github.com/jmoiron/sqlx"
)

var DB *sqlx.DB

func MysqlStart(dsn string) {
	database, err := sqlx.Open("mysql", dsn)
	if err != nil || database == nil {
		fmt.Println("mysql conn - fail-", err)
		return
	} else {
		INFO("mysql server start suc")
	}
	DB = database
}
