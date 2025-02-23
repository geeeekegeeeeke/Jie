package sqlmap

import (
	"fmt"
	"github.com/thoas/go-funk"
	JieOutput "github.com/yhy0/Jie/pkg/output"
	"github.com/yhy0/Jie/pkg/protocols/httpx"
	"github.com/yhy0/Jie/pkg/util"
	"github.com/yhy0/logging"
	"regexp"
	"strings"
	"time"
)

/**
  @author: yhy
  @since: 2023/2/7
  @desc: todo 应该获取全部参数，比如有的 post 请求，但 url 中也有参数
**/

// HeuristicCheckSqlInjection 启发式检测 sql 注入, 先过滤出有效参数，即不存在转型的参数, 之后在进行闭合检测
func (sql *Sqlmap) HeuristicCheckSqlInjection() {
	// 避免POST请求出现参数重名，记录参数位置
	var injectableParamsPos []int

	// 通过闭合字符生成 payload, 看页面是否回显报错信息
	randomTestString := getErrorBasedPreCheckPayload()

	cast := false

	var err error
	var flag bool

	if len(sql.DynamicPara) == 0 {
		flag = true
	}

	errInject := false
	// 检测是否存转型的参数, 转型参数代表无法注入
	for pos, p := range sql.Variations.Params {
		// 如果检测动态参数结果为空，则进行暴力检测，每个参数都检测;若不为空，则只对动态参数进行检测
		if !flag && funk.Contains(sql.DynamicPara, p.Name) {
			flag = true
		}

		if flag {
			payload := sql.Variations.SetPayloadByIndex(p.Index, sql.Url, p.Value+randomTestString, sql.Method)
			logging.Logger.Debugln(payload)

			var res *httpx.Response
			if sql.Method == "GET" {
				res, err = httpx.Request(payload, sql.Method, "", false, sql.Headers)
			} else {
				res, err = httpx.Request(sql.Url, sql.Method, payload, false, sql.Headers)
			}

			time.Sleep(time.Millisecond * 500)

			if err != nil {
				logging.Logger.Debugf("checkIfInjectable Fuzz请求出错")
				continue
			}

			for _, value := range FormatExceptionStrings {
				if funk.Contains(res.Body, value) {
					cast = true
					logging.Logger.Debugf(sql.Url + " 参数: " + p.Name + " 因数值转型无法注入")
					break
				}
			}

			if cast {
				cast = false
				continue
			}

			// todo 这里出现 sql 报错信息则直接认为存在注入点，后期验证交给 sqlmap,误报应该不多吧？
			sql.DBMS = checkDBMSError(sql.Url, p.Name, payload, res)
			//if sql.DBMS != "" {
			//	errInject = true
			//	continue
			//}

			injectableParamsPos = append(injectableParamsPos, pos)
			logging.Logger.Debugf(sql.Url + " 参数: " + p.Name + " 未检测到转型,尝试注入检测")

			// 这里也和 sqlmap 一样 顺手检测一下xss、fi 漏洞
			randStr1, randStr2 := util.RandomLetters(6), util.RandomLetters(6)
			value := fmt.Sprintf("%s%s%s", randStr1, DummyNonSqliCheckAppendix, randStr2)

			payload = sql.Variations.SetPayloadByIndex(p.Index, sql.Url, fmt.Sprintf("%s'%s", p.Value, value), sql.Method)

			if sql.Method == "GET" {
				res, err = httpx.Request(payload, sql.Method, "", false, sql.Headers)
			} else {
				res, err = httpx.Request(sql.Url, sql.Method, payload, false, sql.Headers)
			}
			if err != nil {
				logging.Logger.Debugf(sql.Url, " checkIfInjectable Fuzz请求出错, %v", err)
				continue
			}

			if funk.Contains(res.Body, value) {
				JieOutput.OutChannel <- JieOutput.VulMessage{
					DataType: "web_vul",
					Plugin:   "XSS",
					VulnData: JieOutput.VulnData{
						CreateTime: time.Now().Format("2006-01-02 15:04:05"),
						Target:     sql.Url,
						Method:     sql.Method,
						Ip:         "",
						Param:      p.Name,
						Payload:    fmt.Sprintf("%s \n", value),
					},
					Level: JieOutput.Medium,
				}
			}

			// 检测文件包含
			re := regexp.MustCompile(FiErrorRegex)
			matches := re.FindAllStringSubmatch(res.Body, -1)

			for _, match := range matches {
				if strings.Contains(strings.ToLower(match[0]), strings.ToLower(randStr1)) {
					JieOutput.OutChannel <- JieOutput.VulMessage{
						DataType: "web_vul",
						Plugin:   "FI",
						VulnData: JieOutput.VulnData{
							CreateTime: time.Now().Format("2006-01-02 15:04:05"),
							Target:     sql.Url,
							Method:     sql.Method,
							Ip:         "",
							Param:      p.Name,
							Payload:    fmt.Sprintf("%s %s \n", value, match[0]),
						},
						Level: JieOutput.Critical,
					}
					break
				}
			}

		}

	}

	if len(injectableParamsPos) == 0 && !errInject {
		logging.Logger.Debugf("无可注入参数")
		return
	}

	for _, pos := range injectableParamsPos {
		sql.checkSqlInjection(pos)
	}
}

func (sql *Sqlmap) checkSqlInjection(pos int) {
	for _, closeType := range CloseType {
		if sql.checkBoolBased(pos, closeType) {
			return
		}
	}

	if sql.checkTimeBasedBlind(pos) {
		return
	}

	for _, closeType := range CloseType {
		if sql.checkUnionBased(pos, closeType) {
			return
		}
	}

}
