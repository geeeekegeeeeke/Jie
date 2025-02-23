package sqlmap

import (
	_ "embed"
	"github.com/antlabs/strsim"
	"github.com/beevik/etree"
	"github.com/yhy0/Jie/pkg/input"
	"github.com/yhy0/Jie/pkg/protocols/httpx"
	"github.com/yhy0/Jie/pkg/util"
	"github.com/yhy0/logging"
	"strconv"
	"time"
)

/**
  @author: yhy
  @since: 2023/2/6
  @desc: 提取自 Yakit 插件: 启发式SQL注入检测
**/

//go:embed xml/errors.xml
var errorsXml string

var (
	SimilarityRatio = 0.9  // 页面相似度
	UpperRatioBound = 0.98 // 上边界
	LowerRatioBound = 0.02 // 下边界

	DiffTolerance = 0.05 // 容差

	// MaxDifflibSequenceLength 用于检测页面相似度的最大长度
	MaxDifflibSequenceLength = 10 * 1024 * 1024

	CloseType = map[int]string{0: `'`, 1: `"`, 2: ``, 3: `')`, 4: `")`}

	//CloseType = map[int]string{0: `'`}

	// FormatExceptionStrings 用于检测格式错误的字符串
	FormatExceptionStrings = []string{
		"Type mismatch", "Error converting", "Please enter a", "Conversion failed",
		"String or binary data would be truncated", "Failed to convert", "unable to interpret text value",
		"Input string was not in a correct format", "System.FormatException", "java.lang.NumberFormatException",
		"ValueError: invalid literal", "TypeMismatchException", "CF_SQL_INTEGER", "CF_SQL_NUMERIC",
		"for CFSQLTYPE ", "cfqueryparam cfsqltype", "InvalidParamTypeException",
		"Invalid parameter type", "Attribute validation error for tag", "is not of type numeric",
		"<cfif Not IsNumeric(", "invalid input syntax for integer", "invalid input syntax for type",
		"invalid number", "character to number conversion error", "unable to interpret text value",
		"String was not recognized as a valid", "Convert.ToInt", "cannot be converted to a ",
		"InvalidDataException", "Arguments are of the wrong type",
	}

	// DummyNonSqliCheckAppendix String used for dummy non-SQLi (e.g. XSS) heuristic checks of a tested parameter value
	DummyNonSqliCheckAppendix = "<'\">"

	//FiErrorRegex Regular expression used for recognition of file inclusion errors
	FiErrorRegex = `(?i)[^\n]{0,100}(no such file|failed (to )?open)[^\n]{0,100}`

	// DbmsErrors 用于报错检查的字典
	DbmsErrors = map[string][]string{}
)

type Sqlmap struct {
	Method      string
	Url         string
	RequestBody string
	Headers     map[string]string
	ContentType string
	Variations  *httpx.Variations

	OriginalBody    string // 原始请求页面
	TemplateBody    string // 经过处理去除动态部分的模板页面
	TemplateCode    int
	DynamicPara     []string          // 动态参数
	DynamicMarkings map[string]string // 动态标记内容
	DBMS            string            // 数据库类型
}

func init() {
	// error based 生成字典
	DbmsErrors = make(map[string][]string)
	doc := etree.NewDocument()

	if err := doc.ReadFromString(errorsXml); err != nil {
		logging.Logger.Errorln(err)
	} else {
		root := doc.SelectElement("root")
		for _, dbms := range root.SelectElements("dbms") {
			for _, dbName := range dbms.Attr {
				var errWords []string
				for _, e := range dbms.SelectElements("error") {
					for _, errWord := range e.Attr {
						errWords = append(errWords, errWord.Value)
					}
				}
				DbmsErrors[dbName.Value] = errWords
			}
		}

	}
}

func Scan(c *input.CrawlResult) {
	if c.Method != "GET" && c.Method != "POST" {
		logging.Logger.Debugln("请求方法不支持检测")
		return
	}

	//waf 只判断作为提示信息 不做进一步操作 如果检出存在注入 则可以考虑附加信息
	if len(c.Waf) > 0 {
		logging.Logger.Warnf("heuristics detected that the target is protected by some kind of WAF/IPS(%+v)", c.Waf)
	}

	//做一些前置检查 避免无意义的后续检测
	if c.Resp.StatusCode == 404 {
		logging.Logger.Warnln(c.Target, " 原始请求资源不存在(404) ")
		return
	}

	if len(c.Param) == 0 {
		logging.Logger.Warnln(c.Target, " 无可供注入参数")
		return
	}

	sql := &Sqlmap{
		Url:          c.Url,
		OriginalBody: c.Resp.Body,
		Method:       c.Method,
		Headers:      c.Headers,
		ContentType:  c.ContentType,
		RequestBody:  c.RequestBody,
		TemplateCode: c.Resp.StatusCode,
		TemplateBody: c.Resp.Body, // 先赋值, 确定好相似标记后，再重新赋值，防止为空
		DynamicMarkings: map[string]string{
			"prefix": "",
			"suffix": "",
		},
	}

	sql.TemplateCode = c.Resp.StatusCode

	variations, err := httpx.ParseUri(sql.Url, []byte(sql.RequestBody), sql.Method, sql.ContentType, sql.Headers)
	if err != nil {
		logging.Logger.Debugln(err)
		return
	}
	sql.Variations = variations

	logging.Logger.Debugf("总共测试参数共%d个 %+v", len(variations.Params), variations.Params)

	// 参数预处理，动态参数检测，模板页面
	if !sql.check() {
		logging.Logger.Infoln(c.Target, " 动态页面检测失败")
		return
	}

	//开始启发式、sql注入检测
	sql.HeuristicCheckSqlInjection()

}

// check 检测动态页面，参数
func (sql *Sqlmap) check() bool {
	res, err := httpx.Request(sql.Url, sql.Method, sql.RequestBody, false, sql.Headers)

	if err != nil {
		return false
	}

	if len(res.Body) < MaxDifflibSequenceLength && len(sql.OriginalBody) < MaxDifflibSequenceLength {
		// todo 没有经过大量测试，有待优化
		sim := strsim.Compare(res.Body, sql.OriginalBody)
		if sim < SimilarityRatio {
			logging.Logger.Debugln(sql.Url, " 检测到动态页面, 相似度为：", sim)
			prefix, suffix := findDynamicContent(sql.OriginalBody, res.Body)
			sql.DynamicMarkings["prefix"] = prefix
			sql.DynamicMarkings["suffix"] = suffix

			// 去除请求页面的动态内容，设置模板页面
			sql.TemplateBody = sql.removeDynamicContent(sql.OriginalBody)

		}
	}

	// 动态参数检测
	for _, p := range sql.Variations.Params {
		payload := sql.Variations.SetPayloadByIndex(p.Index, sql.Url, strconv.Itoa(util.RandomNumber(0, 9999)), sql.Method)

		logging.Logger.Debugln(payload)

		if sql.Method == "GET" {
			res, err = httpx.Request(payload, sql.Method, "", false, sql.Headers)
		} else {
			res, err = httpx.Request(sql.Url, sql.Method, payload, false, sql.Headers)
		}

		if err != nil {
			continue
		}

		res.Body = sql.removeDynamicContent(res.Body)

		if strsim.Compare(res.Body, sql.TemplateBody) < SimilarityRatio {
			sql.DynamicPara = append(sql.DynamicPara, p.Name)
			logging.Logger.Debugln("检测到动态参数 ", p.Name)
		}
		time.Sleep(time.Millisecond * 500)
	}

	return true
}
