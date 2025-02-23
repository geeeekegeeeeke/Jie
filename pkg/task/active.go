package task

import (
	"fmt"
	wappalyzer "github.com/projectdiscovery/wappalyzergo"
	"github.com/remeh/sizedwaitgroup"
	"github.com/thoas/go-funk"
	"github.com/yhy0/Jie/conf"
	"github.com/yhy0/Jie/pkg/protocols/httpx"
	"github.com/yhy0/Jie/pkg/util"
	"github.com/yhy0/Jie/scan/fuzz/bbscan"
	"github.com/yhy0/Jie/scan/nuclei"
	"github.com/yhy0/Jie/scan/pocs_go"
	"github.com/yhy0/Jie/scan/pocs_go/log4j"
	"github.com/yhy0/Jie/scan/waf"
	"github.com/yhy0/logging"
	"regexp"
)

/**
  @author: yhy
  @since: 2023/1/11
  @desc: 爬虫主动扫描数据处理
**/

// Active 主动扫描 调用爬虫扫描, 只会输入一个域名
func Active(target string) []string {
	if target == "" {
		logging.Logger.Errorln("target must be set")
		return nil
	}

	// 判断是否以 http https 开头
	httpMatch, _ := regexp.MatchString("^(http)s?://", target)
	if !httpMatch {
		portMatch, _ := regexp.MatchString(":443", target)
		if portMatch {
			target = fmt.Sprintf("https://%s", target)
		} else {
			target = fmt.Sprintf("http://%s", target)
		}
	}

	// 爬虫前，进行连接性、指纹识别、 waf 探测
	resp, err := httpx.Request(target, "GET", "", false, nil)
	if err != nil {
		logging.Logger.Errorln("End: ", err)
		return nil
	}

	var technologies []string

	wappalyzerClient, err := wappalyzer.New()
	fingerprints := wappalyzerClient.Fingerprint(resp.Header, []byte(resp.Body))

	for k, _ := range fingerprints {
		technologies = append(technologies, k)
	}

	//todo 目前只进行目标的 header 探测，后期和爬虫结合
	if funk.Contains(conf.GlobalConfig.WebScan.Plugins, "LOG4J") {
		// log4j
		go func() {
			log4j.Scan(target, "GET", "")
		}()
	}

	t := Task{
		TaskId:      util.UUID(),
		Target:      target,
		Parallelism: 10,
		//Fingerprints: technologies,
	}

	wafs := waf.Scan(target, resp.Body)

	t.wg = sizedwaitgroup.New(t.Parallelism)
	t.limit = make(chan struct{}, t.Parallelism)

	// 爬虫的同时进行指纹识别
	subdomains, dirs := t.Crawler(wafs)

	t.wg.Wait()

	logging.Logger.Debugln("Fingerprints: ", t.Fingerprints)

	if funk.Contains(conf.GlobalConfig.WebScan.Plugins, "BBSCAN") {
		// 先单独进行 bbscan 进行敏感目录扫描，不使用协程
		technologies = bbscan.BBscan(target, "", nil, dirs)
	}

	t.Fingerprints = funk.UniqString(append(t.Fingerprints, technologies...))

	// 一个网站应该只执行一次 POC 检测, poc 检测放到最后
	if funk.Contains(conf.GlobalConfig.WebScan.Plugins, "POC") {
		// 这里根据指纹进行对应的检测
		pocs_go.PocCheck(t.Fingerprints, target, resp.RequestUrl, "")
	}

	if funk.Contains(conf.GlobalConfig.WebScan.Plugins, "NUCLEI") {
		// 这里根据指纹进行对应的检测
		nuclei.Scan(target, t.Fingerprints)
	}

	close(t.limit)
	return subdomains
}
