package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/hanjm/errors"
	"gopkg.in/gomail.v2"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fund
// data example: jsonpgz({"fundcode":"180012","name":"閾跺崕瀵岃涓婚娣峰悎","jzrq":"2019-10-31","dwjz":"3.6009",
// "gsz":"3.6490","gszzl":"1.34","gztime":"2019-11-01 15:00"});
type Fund struct {
	// 基金代码
	FundCode string `json:"fundcode"`
	// 基金名称
	Name string `json:"name"`
	// 截止日期
	JzRq string `json:"jzrq"`
	// (昨日)单位净值 
	Dwjz float64 `json:"dwjz,string"`
	// (当前)估算净值
	Gsz float64 `json:"gsz,string"`
	// 估算增长率
	Gszzl float64 `json:"gszzl,string"`
	// 估值时间
	Gztime string `json:"gztime"`
}

func (f Fund) String() string {
	return fmt.Sprintf("%s-单位净值:%v-估算净值:%v-估算增长率:%v-估值时间:%s-截止日期:%s", f.Name, f.Dwjz, f.Gsz, f.Gszzl, f.Gztime, f.JzRq)
}

var (
	httpClient = &http.Client{}
	bodyPrefix = []byte("jsonpgz(")
	bodySuffix = []byte(");")
)

// FetchFund
func FetchFund(code string) (fund Fund, err error) {
	reqURL := "http://fundgz.1234567.com.cn/js/" + code + ".js"
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		err = errors.Errorf(err, "new request, url:%s", reqURL)
		return
	}
	// 设置一个正常浏览器的ua
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_14_6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/78.0.3904.70 Safari/537.36")
	log.Printf("request url:%s", reqURL)
	resp, err := httpClient.Do(req)
	if err != nil {
		err = errors.Errorf(err, "do request, url:%s", reqURL)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		err = errors.Errorf(err, "read body")
		return
	}
	// 处理body
	body = bytes.TrimPrefix(body, bodyPrefix)
	body = bytes.TrimSuffix(body, bodySuffix)
	err = json.Unmarshal(body, &fund)
	if err != nil {
		err = errors.Errorf(err, "unmarshal, body:%s", body)
		return
	}
	log.Printf("funds:%+v", fund)
	return fund, nil
}

// FetchFunds
func FetchFunds(codes []string) (funds []Fund, err error) {
	const concurrency = 3
	limiter := make(chan struct{}, concurrency)
	foundCh := make(chan Fund)
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(len(codes))
	go func() {
		for _, code := range codes {
			code := code
			limiter <- struct{}{}
			go func() {
				defer func() {
					wg.Done()
					<-limiter
				}()
				if code == "" {
					return
				}
				fund, err := FetchFund(code)
				if err != nil {
					err = errors.Errorf(err, "code:%s", code)
					select {
					case errCh <- err:
					default:
					}
					return
				}
				foundCh <- fund
			}()
		}
	}()
	go func() {
		wg.Wait()
		close(foundCh)
		close(errCh)
	}()
	for fund := range foundCh {
		funds = append(funds, fund)
	}
	// 按跌幅从大到小排序
	sort.Slice(funds, func(i, j int) bool {
		return funds[i].Gszzl+1 < funds[j].Gszzl+1
	})
	return funds, <-errCh
}

// GenerateEmailHTML
func GenerateEmailHTML(funds []Fund, minRiseNum float64, maxFallNum float64) (emailHtml string, shouldSend bool) {
	var elements []string
	var content string
	for _, fund := range funds {
		var status string
		// 涨跌幅度超出设定值
		if fund.Gszzl > 0 && fund.Gszzl >= minRiseNum {
			status = "涨"
		} else if fund.Gszzl < 0 && fund.Gszzl <= maxFallNum {
			status = "跌"
		} else {
			status = "-"
		}
		element := `
            <tr>
              <td width="50" align="center">` + status + `</td>
              <td width="50" align="center">` + fund.Name + `</td>
              <td width="50" align="center">` + strconv.FormatFloat(fund.Gszzl, 'f', -1, 64) + `%</td>
              <td width="50" align="center">` + strconv.FormatFloat(fund.Gsz, 'f', -1, 64) + `</td>
              <td width="50" align="center">` + strconv.FormatFloat(fund.Dwjz, 'f', -1, 64) + `</td>
              <td width="50" align="center">` + fund.Gztime + `</td>
            </tr>
			`
		elements = append(elements, element)
	}
	if len(elements) > 0 {
		content = strings.Join(elements, "\n")
		html := `
			</html>
				<head>
					<meta http-equiv="Content-Type" content="text/html; charset=utf-8" />
				</head>
            <body>
				<div id="container">
					<p>基金涨跌监控:</p>
					<div id="content">
						<table width="30%" border="1" cellspacing="0" cellpadding="0">
							<tr>
							  <td width="50" align="center">状态</td>
							  <td width="100" align="center">基金名称</td>
							  <td width="50" align="center">估算涨幅</td>
							  <td width="50" align="center">当前估算净值</td>
							  <td width="50" align="center">昨日单位净值</td>
							  <td width="50" align="center">估算时间</td>
							</tr>` + content + `
						</table>
					</div>
            	</div>
            </div>
            </body>
        </html>`

		return html, true
	}

	return "", false
}

var (
	timeLocationCST = time.FixedZone("CST", 28800)
)

func SendEmail(content string, smtpHost string, emailName string, emailPassword string, emailTo string) (err error) {
	if content == "" {
		return
	}
	m := gomail.NewMessage()
	m.SetHeader("From", emailName)
	m.SetHeader("To", emailTo)
	m.SetHeader("Subject", fmt.Sprintf("基金涨跌监控-%s", time.Now().In(timeLocationCST).Format(time.RFC3339)))
	m.SetBody("text/html", content)
	d := gomail.NewDialer(smtpHost, 587, emailName, emailPassword)
	if err := d.DialAndSend(m); err != nil {
		err = errors.Errorf(err, "content:%s", content)
		return err
	}
	return nil
}

func init() {
	// errors包默认filter了github.com下包的调用栈
	errors.SetFilterFunc(nil)
}

func main() {
	// log
	log.SetFlags(log.LstdFlags | log.Lshortfile)
	// config
	fundCodes := os.Getenv("FOUND_CODES")
	if fundCodes == "" {
		fundCodes = "163406,519697,180012,003095,519778"
	}
	log.Printf("fundCodes:%+v", fundCodes)
	smtpHost := os.Getenv("SMTP_HOST")
	emailName := os.Getenv("EMAIL_NAME")
	emailPassword := os.Getenv("EMAIL_PASSWORD")
	emailTo := os.Getenv("EMAIL_TO")
	if emailTo == "" {
		emailTo = emailName
	}
	log.Printf("emailTo:%+v", emailTo)
	// fetch funds data
	fundResult, err := FetchFunds(strings.Split(fundCodes, ","))
	if err != nil {
		log.Fatalf("failed to fetch funds, err:%s", err)
		return
	}
	// judge
	const minRiseNum, maxFallNum = 1, -0.8
	content, shouldSend := GenerateEmailHTML(fundResult, minRiseNum, maxFallNum)
	log.Printf("shouldSend:%v", shouldSend)
	if !shouldSend {
		return
	}
	// notify
	err = SendEmail(content, smtpHost, emailName, emailPassword, emailTo)
	if err != nil {
		log.Fatalf("failed to send email, err:%s", err)
		return
	}
}
