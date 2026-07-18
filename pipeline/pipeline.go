package pipeline

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"fkw/libs"
)

// PC 上传客户端版本（与 PC 抓包一致）
const clientVersion = "6.5.3"
const deviceVersion = "0bdb06"
const defaultGoalSteps = 10000
const defaultStepWidth = 70
const defaultWeight = 70.0
const zmRule = "5,6,7,8#3000;17,18,19,20,21,22#4000"

// accountProfile 登录后动态拉取的账号/设备信息
type accountProfile struct {
	SerialPlain string  // 明文 serial，如 068000001017054146001001
	SerialB64   string  // PC 接口用 base64(serial)
	DeviceType  string  // TW726
	Weight      float64
	StepWidth   int
	GoalSteps   int
}

// PC端初始化及轮询对应的响应结构
type InitSidResponse struct {
	Sid string `json:"sid"`
}

type PollResponse struct {
	Status      int    `json:"status"`
	Logo        string `json:"logo"`
	Assesstoken string `json:"assesstoken"`
	Mobile      string `json:"mobile"`
}

type LoginData struct {
	AccessToken string `json:"accessToken"`
	UserId      uint   `json:"userid"`
}

type LoginResponse struct {
	Code string      `json:"resultCode"`
	Data []LoginData `json:"data"`
}

// UploadResp 解析 sync.wanbu.com.cn 的上传接口响应
type UploadResp struct {
	ResultCode  string `json:"resultCode"`
	Status      int    `json:"status"`
	RespMsg     string `json:"respMsg"`
	Commond     string `json:"commond"`
	SequenceID  string `json:"sequenceID"`
	ServerVer   string `json:"serverversion"`
	Aftertime   string `json:"aftertime"`
	Beforetime  string `json:"beforetime"`
	Hmsurl      string `json:"hmsurl"`
	Url         string `json:"url"`
}

// Result 是一次完整跑批的最终返回
type Result struct {
	Ok           bool    `json:"ok"`
	UserId       uint    `json:"userId"`
	Sid          string  `json:"sid"`
	PcToken      string  `json:"pcToken"`
	MobileToken  string  `json:"mobileToken"`
	DeviceSerial string  `json:"deviceSerial"`
	DeviceType   string  `json:"deviceType"`
	Weight       float64 `json:"weight"`
	StepWidth    int     `json:"stepWidth"`
	HTTPStatus   int     `json:"httpStatus"`
	ResultCode   string  `json:"resultCode"`
	ResponseBody string  `json:"responseBody"`
	StepNumber   int     `json:"stepNumber"`
	Err          string  `json:"err,omitempty"`
}

// Run 跑一遍完整的 5 步流程
// logSink 用来把日志回传给 web 层（每行不带换行）
func Run(ctx context.Context, logSink func(string), account, password string, stepNumber int) Result {
	r := Result{StepNumber: stepNumber}
	if logSink == nil {
		logSink = func(string) {}
	}
	logf := func(format string, a ...any) {
		logSink(fmt.Sprintf(format, a...))
	}

	// ---------------------------------------------------------------
	// 客户端
	// ---------------------------------------------------------------
	jar, err := cookiejar.New(nil)
	if err != nil {
		r.Err = "创建 Cookie Jar 失败: " + err.Error()
		return r
	}
	pcClient := &http.Client{Timeout: 15 * time.Second, Jar: jar}
	mobileClient := &http.Client{Timeout: 15 * time.Second}

	// 动态日期：今天 / 昨天 / 前天
	today := time.Now().Format("20060102")
	yesterday := time.Now().AddDate(0, 0, -1).Format("20060102")
	dayBefore := time.Now().AddDate(0, 0, -2).Format("20060102")

	// ==========================================
	// 步骤 1：PC 端初始化 sid
	// ==========================================
	logf("[*] 步骤 1: 正在模拟 PC 端请求初始化会话...")

	initReq, err := http.NewRequestWithContext(ctx, "POST",
		"http://pcsync.wanbu.com.cn/NewWanbu/App/Api/index.php/AutoLogin/returnSidPc", nil)
	if err != nil {
		r.Err = "创建初始化请求失败: " + err.Error()
		return r
	}
	initReq.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	initReq.Header.Set("User-Agent", "PEB_CTRL")
	initReq.Header.Set("Pragma", "no-cache")

	initResp, err := pcClient.Do(initReq)
	if err != nil {
		r.Err = "请求初始化会话失败: " + err.Error()
		return r
	}
	initBytes, _ := io.ReadAll(initResp.Body)
	initResp.Body.Close()
	var initData InitSidResponse
	if err := json.Unmarshal([]byte(strings.TrimLeft(string(initBytes), "\xef\xbb\xbf")), &initData); err != nil {
		r.Err = "解析 sid 失败: " + err.Error()
		return r
	}
	dynamicSid := initData.Sid
	r.Sid = dynamicSid
	logf("[+] 成功动态获取到会话 SID: %s", dynamicSid)

	// ==========================================
	// 步骤 2：移动端登录（带 authName 优先；失败则降级裸账号密码）
	// ==========================================
	logf("[*] 步骤 2: 正在向移动端 wapjava 后端发起登录...")

	type loginReqBody struct {
		AuthName     string `json:"authName,omitempty"`
		AuthPassword string `json:"authPassword,omitempty"`
		Password     string `json:"password"`
		Phonebrand   string `json:"phonebrand"`
		Phonemodel   string `json:"phonemodel"`
		Username     string `json:"username"`
	}
	buildLogin := func(withAuth bool) *loginReqBody {
		rb := &loginReqBody{
			Password:   password,
			Phonebrand: "Redmi",
			Phonemodel: "23117RK66C",
			Username:   account,
		}
		if withAuth {
			rb.AuthName = "wanbu"
			rb.AuthPassword = "16d11a76e75d1c8b4710fe64083ca671"
		}
		return rb
	}

	doLogin := func(withAuth bool) (LoginResponse, string, error) {
		jsonBytes, _ := json.Marshal(buildLogin(withAuth))
		loginForm := url.Values{}
		loginForm.Set("ReqMessageBody", string(jsonBytes))
		const wapjava = "https://wapjava.wanbu.com.cn"
		req, _ := http.NewRequestWithContext(ctx, "POST",
			wapjava+"/phoneServer/Login_Reguser_Service/GetUserLogin",
			strings.NewReader(loginForm.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("version", "1.0")
		req.Header.Set("clientname", "iWanbuAndroid_")
		req.Header.Set("clientversion", "7.2.4.6175")
		req.Header.Set("User-Agent", "okhttp/4.11.0")

		resp, err := mobileClient.Do(req)
		if err != nil {
			return LoginResponse{}, "", err
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		bodyStr := strings.TrimSpace(strings.TrimLeft(string(body), "\xef\xbb\xbf"))
		var lr LoginResponse
		if err := json.Unmarshal([]byte(bodyStr), &lr); err != nil {
			return LoginResponse{}, bodyStr, fmt.Errorf("解析登录 JSON 失败: %v (原文: %s)", err, bodyStr)
		}
		return lr, bodyStr, nil
	}

	loginResp, rawBody, err := doLogin(true)
	if err != nil {
		r.Err = "登录失败: " + err.Error()
		return r
	}
	logf("    [debug] 登录 HTTP 响应长度=%d, 前 200 字: %s", len(rawBody), preview(rawBody, 200))
	logf("    [debug] resultCode=%s, data 元素数=%d", loginResp.Code, len(loginResp.Data))
	if len(loginResp.Data) < 1 {
		logf("[-] 带 authName 登录未拿到 data (resultCode=%s)，尝试降级到裸账号密码...", loginResp.Code)
		loginResp, rawBody, err = doLogin(false)
		if err != nil {
			r.Err = "登录失败: " + err.Error()
			return r
		}
		logf("    [debug] 降级登录 HTTP 响应长度=%d, 前 200 字: %s", len(rawBody), preview(rawBody, 200))
		logf("    [debug] 降级 resultCode=%s, data 元素数=%d", loginResp.Code, len(loginResp.Data))
	}
	if len(loginResp.Data) < 1 {
		r.Err = fmt.Sprintf("登录失败：返回数据为空 (resultCode=%s, 响应: %s)", loginResp.Code, preview(rawBody, 300))
		return r
	}
	mobileToken := loginResp.Data[0].AccessToken
	userId := loginResp.Data[0].UserId
	r.MobileToken = mobileToken
	r.UserId = userId
	logf("[+] 登录成功, userid=%d", userId)

	// ==========================================
	// 步骤 2.5：自动拉用户资料 + 绑定设备
	//   GetUserInfo        → weight / stepwidth / stepgoal
	//   getUserDeviceNew   → deviceserial / devicemode (cap.har)
	// ==========================================
	logf("[*] 步骤 2.5: 自动获取用户资料与绑定设备...")
	prof, profErr := fetchAccountProfile(ctx, mobileClient, mobileToken, userId, logf)
	if profErr != nil {
		r.Err = "获取设备信息失败: " + profErr.Error()
		return r
	}
	r.DeviceSerial = prof.SerialPlain
	r.DeviceType = prof.DeviceType
	r.Weight = prof.Weight
	r.StepWidth = prof.StepWidth
	logf("[+] 设备 serial=%s type=%s weight=%.1f stepWidth=%d goal=%d",
		prof.SerialPlain, prof.DeviceType, prof.Weight, prof.StepWidth, prof.GoalSteps)

	// ==========================================
	// 步骤 3：扫码确认 status/1
	// ==========================================
	logf("[*] 步骤 3: 正在向扫码授权接口提交扫描 (SID: %s)...", dynamicSid)

	scanFormData := url.Values{}
	scanFormData.Set("accessToken", mobileToken)
	scanFormData.Set("userid", fmt.Sprintf("%d", userId))
	scanFormData.Set("sid", dynamicSid)

	doScanStep := func(status int) (string, error) {
		req, _ := http.NewRequestWithContext(ctx, "POST",
			fmt.Sprintf("https://wap.wanbu.com.cn/NewWanbu/App/Api/index.php/AutoLogin/scanlogin/status/%d", status),
			strings.NewReader(scanFormData.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("User-Agent", "android-async-http/1.4.4 (http://loopj.com/android-async-http)")
		resp, err := mobileClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return strings.TrimSpace(strings.TrimLeft(string(b), "\xef\xbb\xbf")), nil
	}

	scanResult, err := doScanStep(1)
	if err != nil {
		r.Err = "扫码扫描请求失败: " + err.Error()
		return r
	}
	logf("[+] 后端授权响应结果 (status/1 扫描): %s", scanResult)

	// ==========================================
	// 步骤 3.5：扫码确认 status/2
	// ==========================================
	logf("[*] 步骤 3.5: 正在向扫码授权接口提交最终确认 (status/2)...")
	confirmResult, err := doScanStep(2)
	if err != nil {
		r.Err = "最终确认请求失败: " + err.Error()
		return r
	}
	logf("[+] 后端授权响应结果 (status/2 确认): %s", confirmResult)

	// ==========================================
	// 步骤 4：PC 端轮询拿 PC 端 assesstoken
	// ==========================================
	logf("[*] 步骤 4: 正在模拟 PC 端收尾轮询获取同步 Token...")
	pollURL := fmt.Sprintf("http://pcsync.wanbu.com.cn/NewWanbu/App/Api/index.php/AutoLogin/poll/PCClient?sid=%s&loginType=1", dynamicSid)

	var pollData PollResponse
	for attempt := 1; attempt <= 30; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", pollURL, nil)
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
		req.Header.Set("User-Agent", "PEB_CTRL")
		resp, err := pcClient.Do(req)
		if err != nil {
			r.Err = fmt.Sprintf("轮询失败: %v", err)
			return r
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		bodyStr := strings.TrimLeft(string(body), "\xef\xbb\xbf")
		pd := PollResponse{}
		if err := json.Unmarshal([]byte(bodyStr), &pd); err != nil {
			r.Err = fmt.Sprintf("解析轮询 JSON 失败: %v\n原文: %s", err, bodyStr)
			return r
		}
		pollData = pd
		logf("    [轮询 %02d] status=%d %s", attempt, pd.Status, pd.Logo)
		if pd.Status == 2 && pd.Assesstoken != "" {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if pollData.Status != 2 || pollData.Assesstoken == "" {
		r.Err = fmt.Sprintf("轮询超时：移动端始终未完成扫码确认，最终 status=%d", pollData.Status)
		return r
	}
	pcAccessToken := pollData.Assesstoken
	r.PcToken = pcAccessToken
	// 抓包/实测：newUploadData.accessToken 必须是 PC poll assesstoken
	// base64 解码后 "&" 前的 32-hex，不是手机登录 mobileToken。
	// 例: base64(hex&userid&昵称) → hex
	uploadToken := extractPCUploadToken(pcAccessToken)
	if uploadToken == "" {
		uploadToken = mobileToken
		logf("[-] 无法从 PC assesstoken 提取 32-hex，回退 mobileToken")
	}
	logf("[+] 成功拦截 PC 端上传 Token(raw)=%s", pcAccessToken)
	logf("[+] 上传用 accessToken(32-hex)=%s", uploadToken)

	// 后续上传全程使用动态设备信息
	deviceSerial := prof.SerialB64
	deviceType := prof.DeviceType
	accountWeight := prof.Weight
	stepWidthCM := prof.StepWidth
	goalSteps := prof.GoalSteps

	// ==========================================
	// 步骤 5.0：BindQuery → 拿 lastdayid / lasthourid 作为 package 游标
	// 抓包顺序: BindQuery → newUploadData → PedDataSync → recipeDownLoad
	// ==========================================
	logf("[*] 步骤 5.0: BindQuery (serialB64=%s)...", deviceSerial)
	const syncBase = "http://sync.wanbu.com.cn"
	upClient := libs.NewClient(syncBase, 15*time.Second)
	bindSequenceID := fmt.Sprintf("%d", time.Now().Unix())

	// dayPackage=lastdayid+1, hourPackage≈lasthourid+30
	dayPackage := "1"
	hourPackage := "30"
	lastDayID, lastHourID := 0, 0

	bindBody := map[string]any{
		"clientvison":    clientVersion,
		"commond":        "BindQuery",
		"deviceType":     deviceType,
		"deviceserial":   deviceSerial,
		"reqservicetype": 0,
		"sequenceID":     bindSequenceID,
		"timezone":       "0",
	}
	bindJSON, _ := json.Marshal(bindBody)
	bindForm := url.Values{}
	bindForm.Set("commond", "BindQuery")
	bindForm.Set("ReqMessageBody", string(bindJSON))
	bindReq, _ := http.NewRequestWithContext(ctx, "POST",
		syncBase+"/WanbuDataServer_NEW/PCPedUploadFlowsService",
		strings.NewReader(bindForm.Encode()))
	setPCHeaders(bindReq)
	if bindResp, bindErr := upClient.GetHTTPClient().Do(bindReq); bindErr == nil {
		bb, _ := io.ReadAll(bindResp.Body)
		bindResp.Body.Close()
		bindText := decodeSyncBody(bb)
		logf("[+] BindQuery HTTP=%d: %s", bindResp.StatusCode, preview(bindText, 240))
		if d, h, ok := parseBindCursor(bindText); ok {
			lastDayID, lastHourID = d, h
			dayPackage = strconv.Itoa(d + 1)
			hourPackage = strconv.Itoa(h + 30) // 抓包 560→590
			logf("[+] 游标 lastdayid=%d lasthourid=%d → dayPackage=%s hourPackage=%s",
				d, h, dayPackage, hourPackage)
		} else {
			logf("[-] 未能解析 BindQuery 游标，使用默认 dayPackage=%s hourPackage=%s", dayPackage, hourPackage)
		}
	} else {
		logf("[-] BindQuery 失败: %v（继续用默认游标）", bindErr)
	}

	// ==========================================
	// 步骤 5.1：按抓包结构上传 4 天达标数据
	// zmrule 朝 5-8#3000 / 暮 17-22#4000；小时数据与 zmstatus=1,1 自洽
	// ==========================================
	if stepNumber < goalSteps {
		stepNumber = goalSteps + 1000
	}
	r.StepNumber = stepNumber
	logf("[*] 步骤 5.1: 上传达标报文 (today=%s steps=%d dayPkg=%s hourPkg=%s)...",
		today, stepNumber, dayPackage, hourPackage)

	uploadSeq := fmt.Sprintf("%d", time.Now().Unix())
	// 4 天：今天-3 .. 今天（与抓包 list 长度一致）
	dates := []string{
		time.Now().AddDate(0, 0, -3).Format("20060102"),
		dayBefore,
		yesterday,
		today,
	}
	// 每天略有波动，更像真机
	daySteps := []int{
		max(goalSteps, stepNumber-300),
		max(goalSteps, stepNumber+200),
		max(goalSteps, stepNumber+800),
		stepNumber,
	}

	listRecipe := make([]map[string]any, 0, 4)
	listDay := make([]map[string]any, 0, 4)
	listHour := make([]map[string]any, 0, 4)
	for i, date := range dates {
		steps := daySteps[i]
		listRecipe = append(listRecipe, recipeEntryCapture(date))
		listDay = append(listDay, makeDayEntryCapture(date, steps, goalSteps, stepWidthCM, accountWeight))
		listHour = append(listHour, makeHourEntryCapture(date, steps, goalSteps, stepWidthCM))
	}

	payloadMap := map[string]any{
		"accessToken":    uploadToken,
		"cachedata":      0,
		"clientlanguage": "Chinese",
		"clientvison":    clientVersion,
		"commond":        "newUploadData",
		"dayPackage":     dayPackage,
		"deviceType":     deviceType,
		"deviceserial":   deviceSerial,
		"hourPackage":    hourPackage,
		"listRecipeData": listRecipe,
		"listday":        listDay,
		"listhour":       listHour,
		"reqservicetype": "0",
		"sequenceID":     uploadSeq,
	}
	_ = lastDayID
	_ = lastHourID

	payloadJSON, err := json.Marshal(payloadMap)
	if err != nil {
		r.Err = "序列化上传 Payload 失败: " + err.Error()
		return r
	}

	uploadForm := url.Values{}
	uploadForm.Set("commond", "pcUploadData")
	uploadForm.Set("ReqMessageBody", string(payloadJSON))

	req, err := http.NewRequestWithContext(ctx, "POST",
		syncBase+"/WanbuDataServer_NEW/PCPedUploadFlowsService",
		strings.NewReader(uploadForm.Encode()))
	if err != nil {
		r.Err = "创建上传请求失败: " + err.Error()
		return r
	}
	setPCHeaders(req)

	resp, err := upClient.GetHTTPClient().Do(req)
	if err != nil {
		r.Err = "伪造数据上传失败: " + err.Error()
		return r
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyText := decodeSyncBody(bodyBytes)

	r.HTTPStatus = resp.StatusCode
	r.ResponseBody = bodyText
	r.Ok = resp.StatusCode >= 200 && resp.StatusCode < 300
	r.StepNumber = stepNumber

	var ur UploadResp
	if err := json.Unmarshal([]byte(bodyText), &ur); err == nil {
		r.ResultCode = ur.ResultCode
		switch ur.ResultCode {
		case "0", "0000":
			r.Ok = true
		default:
			r.Ok = false
		}
	}

	logf("[+] 上传 HTTP=%d resultCode=%q", resp.StatusCode, r.ResultCode)
	logf("[+] 上传响应: %s", preview(bodyText, 400))

	// sequenceID：优先用服务端回传，否则用本地 uploadSeq
	seqForFollow := ur.SequenceID
	if seqForFollow == "" {
		seqForFollow = uploadSeq
	}

	// =========================================
	// 步骤 5.5：PedDataSync + recipeDownLoad（serial 全程一致）
	// =========================================
	if ur.ResultCode == "0000" || ur.ResultCode == "0" {
		logf("[*] 步骤 5.5: PedDataSync + recipeDownLoad...")

		syncBody := map[string]any{
			"accessToken":    "",
			"clientname":     "PCClient",
			"clientvison":    clientVersion,
			"commond":        "PedDataSync",
			"deviceType":     deviceType,
			"deviceserial":   deviceSerial,
			"deviceversion":  deviceVersion,
			"eveningBegin":   "17",
			"eveningEnd":     "22",
			"eveningStepNum": "4000",
			"goalStepNum":    goalSteps,
			"morningBegin":   "5",
			"morningEnd":     "8",
			"morningStepNum": "3000",
			"pedmodel":       "-1",
			"reqservicetype": 0,
			"sequenceID":     seqForFollow,
			"stepwith":       stepWidthCM,
			"timezone":       "0",
			"weight":         accountWeight,
		}
		syncOK, syncResp := postCommond(ctx, upClient, "PedDataSync", syncBody, logf)
		if syncOK {
			logf("[+] PedDataSync ok respMsg=%q", syncResp.RespMsg)
		} else {
			logf("[-] PedDataSync 未成功")
		}

		recipeBody := map[string]any{
			"accessToken":    "",
			"clientvison":    clientVersion,
			"commond":        "recipeDownLoad",
			"deviceType":     deviceType,
			"deviceserial":   deviceSerial,
			"reqservicetype": 0,
			"sequenceID":     seqForFollow,
		}
		recipeOK, _ := postCommond(ctx, upClient, "recipeDownLoad", recipeBody, logf)
		if recipeOK {
			logf("[+] recipeDownLoad ok")
		} else {
			logf("[-] recipeDownLoad 未成功")
		}

		// 抓包里 PedDataSync 可 1003，上传 0000 即已落库
		if r.ResultCode == "0000" || r.ResultCode == "0" {
			r.Ok = true
			logf("[+] 上传成功 stepNumber=%d userid=%d (PedDataSync=%v recipe=%v)",
				stepNumber, userId, syncOK, recipeOK)
		}
	} else {
		logf("[-] newUploadData 非 0000，跳过后续握手 (resultCode=%q)", ur.ResultCode)
	}
	return r
}

func setPCHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("User-Agent", "PEB_CTRL")
	req.Header.Set("Pragma", "no-cache")
	req.Host = "sync.wanbu.com.cn"
}

// extractPCUploadToken: poll 返回的 assesstoken 是 base64("32hex&userid&昵称")
// 上传接口只要前面的 32 位十六进制。
func extractPCUploadToken(assesstoken string) string {
	assesstoken = strings.TrimSpace(assesstoken)
	if assesstoken == "" {
		return ""
	}
	// 已是 32-hex
	if len(assesstoken) == 32 && isHex32(assesstoken) {
		return assesstoken
	}
	raw, err := base64.StdEncoding.DecodeString(assesstoken)
	if err != nil {
		// 容错：url-safe base64
		raw, err = base64.URLEncoding.DecodeString(assesstoken)
		if err != nil {
			return ""
		}
	}
	s := string(raw)
	if i := strings.IndexByte(s, '&'); i > 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if isHex32(s) {
		return s
	}
	if len(s) >= 32 && isHex32(s[:32]) {
		return s[:32]
	}
	return ""
}

func isHex32(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < 32; i++ {
		c := s[i]
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

// decodeSyncBody 处理 chunked/base64 或纯 JSON 响应
func decodeSyncBody(raw []byte) string {
	s := strings.TrimSpace(strings.TrimLeft(string(raw), "\xef\xbb\xbf"))
	if s == "" {
		return s
	}
	if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
		return s
	}
	// chunked 文本：跳过长度行，拼 base64
	var b64 strings.Builder
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line == "0" {
			continue
		}
		if _, err := strconv.ParseInt(line, 16, 64); err == nil && len(line) <= 8 {
			continue
		}
		b64.WriteString(line)
	}
	if b64.Len() == 0 {
		return s
	}
	if dec, err := base64.StdEncoding.DecodeString(b64.String()); err == nil {
		out := strings.TrimSpace(string(dec))
		if strings.HasPrefix(out, "{") {
			return out
		}
	}
	if dec, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(s, "\n", "")); err == nil {
		out := strings.TrimSpace(string(dec))
		if strings.HasPrefix(out, "{") {
			return out
		}
	}
	return s
}

func parseBindCursor(body string) (lastdayid, lasthourid int, ok bool) {
	// 可能是 {"data":[{...}]} 或 直接对象
	var wrap struct {
		Data []struct {
			LastDayID  json.Number `json:"lastdayid"`
			LastHourID json.Number `json:"lasthourid"`
		} `json:"data"`
		LastDayID  json.Number `json:"lastdayid"`
		LastHourID json.Number `json:"lasthourid"`
	}
	if err := json.Unmarshal([]byte(body), &wrap); err != nil {
		return 0, 0, false
	}
	pick := func(n json.Number) int {
		if n == "" {
			return 0
		}
		v, _ := n.Int64()
		return int(v)
	}
	if len(wrap.Data) > 0 {
		d, h := pick(wrap.Data[0].LastDayID), pick(wrap.Data[0].LastHourID)
		if d > 0 || h > 0 {
			return d, h, true
		}
	}
	d, h := pick(wrap.LastDayID), pick(wrap.LastHourID)
	if d > 0 || h > 0 {
		return d, h, true
	}
	return 0, 0, false
}

// fetchAccountProfile 登录后拉 GetUserInfo + getUserDeviceNew
func fetchAccountProfile(ctx context.Context, client *http.Client, token string, userId uint, logf func(string, ...any)) (accountProfile, error) {
	prof := accountProfile{
		DeviceType: "TW726",
		Weight:     defaultWeight,
		StepWidth:  defaultStepWidth,
		GoalSteps:  defaultGoalSteps,
	}

	// ---- GetUserInfo ----
	infoURL := fmt.Sprintf(
		"https://wapjava.wanbu.com.cn/phoneServer/Login_Reguser_Service/GetUserInfo?clientName=iWanbuAndroid_&accessToken=%s&clientVersion=7.2.4.6175&userid=%d&version=1.0",
		url.QueryEscape(token), userId)
	infoReq, _ := http.NewRequestWithContext(ctx, "GET", infoURL, nil)
	infoReq.Header.Set("userid", fmt.Sprintf("%d", userId))
	infoReq.Header.Set("clientversion", "7.2.4.6175")
	infoReq.Header.Set("clientname", "iWanbuAndroid_")
	infoReq.Header.Set("accesstoken", token)
	infoReq.Header.Set("version", "1.0")
	infoReq.Header.Set("User-Agent", "okhttp/4.11.0")
	if resp, err := client.Do(infoReq); err == nil {
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		body := strings.TrimSpace(strings.TrimLeft(string(b), "\xef\xbb\xbf"))
		logf("    [GetUserInfo] %s", preview(body, 200))
		var ur struct {
			Code string `json:"resultCode"`
			Data []struct {
				Weight    float64 `json:"weight"`
				StepWidth int     `json:"stepwidth"`
				StepGoal  int     `json:"stepgoal"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(body), &ur) == nil && len(ur.Data) > 0 {
			if ur.Data[0].Weight > 0 {
				prof.Weight = ur.Data[0].Weight
			}
			if ur.Data[0].StepWidth > 0 {
				prof.StepWidth = ur.Data[0].StepWidth
			}
			if ur.Data[0].StepGoal > 0 {
				prof.GoalSteps = ur.Data[0].StepGoal
			}
		}
	} else {
		logf("    [-] GetUserInfo 失败: %v（用默认体重/步幅）", err)
	}

	// ---- getUserDeviceNew (cap.har)，偶发 502，重试 3 次 ----
	var dr struct {
		Code string `json:"resultCode"`
		Data []struct {
			Device []struct {
				DeviceSerial string `json:"deviceserial"`
				DeviceMode   string `json:"devicemode"`
				DeviceType   string `json:"devicetype"`
			} `json:"device"`
		} `json:"data"`
	}
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		form := url.Values{}
		form.Set("userid", fmt.Sprintf("%d", userId))
		devReq, _ := http.NewRequestWithContext(ctx, "POST",
			"https://wap.wanbu.com.cn/NewWanbu/App/Api/index.php/AppV5/getUserDeviceNew/",
			strings.NewReader(form.Encode()))
		devReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		devReq.Header.Set("clientVersion", "7.2.4.6175")
		devReq.Header.Set("clientName", "iWanbuAndroid_")
		devReq.Header.Set("accessToken", token)
		devReq.Header.Set("version", "1.0")
		devReq.Header.Set("User-Agent", "okhttp/4.11.0")
		devResp, err := client.Do(devReq)
		if err != nil {
			lastErr = fmt.Errorf("网络错误: %w", err)
			logf("    [-] getUserDeviceNew 第%d次失败: %v", attempt, err)
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}
		db, _ := io.ReadAll(devResp.Body)
		devResp.Body.Close()
		devBody := strings.TrimSpace(strings.TrimLeft(string(db), "\xef\xbb\xbf"))
		logf("    [getUserDeviceNew #%d] HTTP=%d %s", attempt, devResp.StatusCode, preview(devBody, 300))
		if !strings.HasPrefix(devBody, "{") {
			lastErr = fmt.Errorf("非 JSON 响应 HTTP=%d", devResp.StatusCode)
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}
		if err := json.Unmarshal([]byte(devBody), &dr); err != nil {
			lastErr = fmt.Errorf("解析失败: %v", err)
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}
		if dr.Code != "0000" && dr.Code != "0" {
			lastErr = fmt.Errorf("resultCode=%s", dr.Code)
			time.Sleep(time.Duration(attempt) * time.Second)
			continue
		}
		lastErr = nil
		break
	}
	if lastErr != nil {
		return prof, fmt.Errorf("getUserDeviceNew 失败: %v", lastErr)
	}
	if len(dr.Data) == 0 || len(dr.Data[0].Device) == 0 {
		return prof, fmt.Errorf("账号未绑定任何设备")
	}
	// 优先 TW 计步器
	chosen := dr.Data[0].Device[0]
	for _, d := range dr.Data[0].Device {
		if strings.EqualFold(d.DeviceType, "TW") || strings.HasPrefix(strings.ToUpper(d.DeviceMode), "TW") {
			chosen = d
			break
		}
	}
	if chosen.DeviceSerial == "" {
		return prof, fmt.Errorf("设备列表无 serial")
	}
	prof.SerialPlain = chosen.DeviceSerial
	prof.SerialB64 = base64.StdEncoding.EncodeToString([]byte(chosen.DeviceSerial))
	if chosen.DeviceMode != "" {
		prof.DeviceType = chosen.DeviceMode
	}
	return prof, nil
}

// recipeEntryCapture 抓包: task1-3=1, task4-8=2
func recipeEntryCapture(date string) map[string]any {
	return map[string]any{
		"recipenumber": 9999,
		"task1state":   1,
		"task2state":   1,
		"task3state":   1,
		"task4state":   2,
		"task5state":   2,
		"task6state":   2,
		"task7state":   2,
		"task8state":   2,
		"walkdate":     date,
	}
}

// makeDayEntryCapture 按抓包比例生成日包；zmstatus=1,1 表示朝暮都达标
func makeDayEntryCapture(date string, steps, goalSteps, stepWidthCM int, weight float64) map[string]any {
	if steps < goalSteps {
		steps = goalSteps
	}
	// 抓包: remain ≈ 2900-3500，不是 step-10000
	remain := 3000 + (steps % 500)
	fast := int(float64(steps) * 0.47)
	return map[string]any{
		"calorieConsumed":      round2(float64(steps) * 0.0276),
		"exerciseAmount":       round2(float64(steps) * 0.00048),
		"faststepnum":          fast,
		"fatConsumed":          round2(float64(steps) * 0.0040),
		"goalStepNum":          goalSteps,
		"remaineffectiveSteps": remain,
		"stepNumber":           steps,
		"stepWidth":            stepWidthCM,
		"walkDistance":         steps * stepWidthCM,
		"walkTime":             max(1, int(float64(steps)*0.0085)),
		"walkdate":             date,
		"weight":               weight,
		"zmrule":               zmRule,
		"zmstatus":             "1,1",
	}
}

// makeHourEntryCapture 把步数分到 朝(6-8) + 暮(17-21)，与 zmstatus=1,1 自洽
func makeHourEntryCapture(walkdate string, total, goalSteps, stepWidthCM int) map[string]any {
	if total < goalSteps {
		total = goalSteps
	}
	// 朝约 45%，暮约 40%，其余 15% 散在白天
	morning := int(float64(total) * 0.45)
	evening := int(float64(total) * 0.40)
	mid := total - morning - evening
	if morning < 3200 {
		d := 3200 - morning
		morning = 3200
		if mid >= d {
			mid -= d
		} else {
			evening -= d - mid
			mid = 0
		}
	}
	if evening < 4200 {
		d := 4200 - evening
		evening = 4200
		if mid >= d {
			mid -= d
		} else {
			morning -= d - mid
			mid = 0
		}
	}

	m6 := int(float64(morning) * 0.28)
	m7 := int(float64(morning) * 0.50)
	m8 := morning - m6 - m7
	n10 := int(float64(mid) * 0.30)
	n11 := int(float64(mid) * 0.25)
	n14 := int(float64(mid) * 0.20)
	n15 := mid - n10 - n11 - n14
	e17 := int(float64(evening) * 0.10)
	e18 := int(float64(evening) * 0.35)
	e19 := int(float64(evening) * 0.25)
	e20 := int(float64(evening) * 0.18)
	e21 := evening - e17 - e18 - e19 - e20

	filled := map[string]string{
		"hour6":  formatHourLine(m6, stepWidthCM),
		"hour7":  formatHourLine(m7, stepWidthCM),
		"hour8":  formatHourLine(m8, stepWidthCM),
		"hour10": formatHourLine(n10, stepWidthCM),
		"hour11": formatHourLine(n11, stepWidthCM),
		"hour14": formatHourLine(n14, stepWidthCM),
		"hour15": formatHourLine(n15, stepWidthCM),
		"hour17": formatHourLine(e17, stepWidthCM),
		"hour18": formatHourLine(e18, stepWidthCM),
		"hour19": formatHourLine(e19, stepWidthCM),
		"hour20": formatHourLine(e20, stepWidthCM),
		"hour21": formatHourLine(e21, stepWidthCM),
	}
	return withAllHours(filled, walkdate)
}

func splitStepsIntoHours(total int, hours []int) []int {
	if len(hours) == 0 || total <= 0 {
		return nil
	}
	weights := []float64{0.16, 0.22, 0.25, 0.27, 0.10}
	if len(weights) != len(hours) {
		each := total / len(hours)
		out := make([]int, len(hours))
		for i := range out {
			out[i] = each
		}
		return out
	}
	assigned := 0
	out := make([]int, len(hours))
	for i, w := range weights {
		v := int(float64(total) * w)
		out[i] = v
		assigned += v
	}
	if diff := total - assigned; diff != 0 && len(out) > 0 {
		out[0] += diff
	}
	return out
}

// formatHourLine: "步数,距离,快走步数,快走距离,慢走步数,慢走距离"
func formatHourLine(steps, widthCM int) string {
	if steps <= 0 {
		return "0,0,0,0,0,0"
	}
	distance := steps * widthCM
	fastSteps := int(float64(steps) * 0.52)
	fastDistance := fastSteps * widthCM
	slowSteps := steps - fastSteps
	slowDistance := slowSteps * widthCM
	return fmt.Sprintf("%d,%d,%d,%d,%d,%d",
		steps, distance, fastSteps, fastDistance, slowSteps, slowDistance)
}

func withAllHours(filled map[string]string, walkdate string) map[string]any {
	out := make(map[string]any, 28)
	for h := 0; h <= 25; h++ {
		key := fmt.Sprintf("hour%d", h)
		if v, ok := filled[key]; ok {
			out[key] = v
		} else {
			out[key] = "0,0,0,0,0,0"
		}
	}
	out["walkdate"] = walkdate
	return out
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

func preview(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// doHandshake 走 219 落库握手（旧版，保留作 fallback）
//
// 服务器在第一次 newUploadData 后，如果 resultCode=1003，会把
// { "commond": "219", "respMsg": "<token>" } 回给客户端。
// 客户端必须立刻把 respMsg 当作 token 再发一次 POST，服务器才
// 会把上传的 listday 真正写入当天的步数聚合表。
//
// 这里我们用一个最小的握手 payload，只带 sequenceID + respMsg，
// 这样如果握手字段名不对，从响应里能直接看到服务器对字段的反馈。
//
//nolint:unused // 保留以备未来"resultCode=1003 时需要单独握手"的分支使用
func doHandshake(ctx context.Context, client *libs.Client, sequenceID, respMsgToken string, logf func(string, ...any)) bool {
	const syncBase = "http://sync.wanbu.com.cn"

	// 去掉 respMsg 末尾可能的换行（抓包里看到带 "\n"）
	token := strings.TrimRight(respMsgToken, "\r\n\t ")

	// 抓包结构：commond=219 走的是同一个接口，ReqMessageBody 里只放
	// 引用上一次的 sequenceID 和握手 token；服务器看到这两个字段
	// 就把上一条 newUploadData 的步数落到今天的日聚合表。
	handshakeBody := map[string]any{
		"commond":        "219",
		"sequenceID":     sequenceID,
		"respMsg":        token,
		"reqservicetype": "0",
	}
	hbJSON, err := json.Marshal(handshakeBody)
	if err != nil {
		logf("    [握手] 序列化失败: %v", err)
		return false
	}

	form := url.Values{}
	form.Set("commond", "219")
	form.Set("ReqMessageBody", string(hbJSON))

	req, err := http.NewRequestWithContext(ctx, "POST",
		syncBase+"/WanbuDataServer_NEW/PCPedUploadFlowsService",
		strings.NewReader(form.Encode()))
	if err != nil {
		logf("    [握手] 创建请求失败: %v", err)
		return false
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("User-Agent", "PEB_CTRL")
	req.Header.Set("Pragma", "no-cache")
	req.Host = "sync.wanbu.com.cn"

	resp, err := client.GetHTTPClient().Do(req)
	if err != nil {
		logf("    [握手] 网络错误: %v", err)
		return false
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyText := strings.TrimSpace(strings.TrimLeft(string(bodyBytes), "\xef\xbb\xbf"))

	logf("    [握手] HTTP 状态: %d, 响应: %s", resp.StatusCode, bodyText)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false
	}
	var hr UploadResp
	if err := json.Unmarshal([]byte(bodyText), &hr); err == nil {
		logf("    [握手] resultCode=%q, respMsg=%q", hr.ResultCode, hr.RespMsg)
		switch hr.ResultCode {
		case "0", "0000", "1000":
			return true
		case "1003":
			// 服务器仍然在要求握手，握手字段名可能不对
			return false
		}
	}
	// 解析不到 resultCode 但 HTTP 200，乐观地认为成功
	return true
}

// postCommond 是给 PedDataSync / recipeDownLoad 这种"握手型"接口用的
// 通用 POST 工具。它把 ReqMessageBody 序列化成 JSON、加上表单外层
// 的 commond= 字段，发送到 sync.wanbu.com.cn 的同一个端点。
func postCommond(ctx context.Context, client *libs.Client, commond string, body map[string]any, logf func(string, ...any)) (bool, UploadResp) {
	const syncBase = "http://sync.wanbu.com.cn"

	hbJSON, err := json.Marshal(body)
	if err != nil {
		logf("    [%s] 序列化失败: %v", commond, err)
		return false, UploadResp{}
	}

	form := url.Values{}
	form.Set("commond", commond)
	form.Set("ReqMessageBody", string(hbJSON))

	req, err := http.NewRequestWithContext(ctx, "POST",
		syncBase+"/WanbuDataServer_NEW/PCPedUploadFlowsService",
		strings.NewReader(form.Encode()))
	if err != nil {
		logf("    [%s] 创建请求失败: %v", commond, err)
		return false, UploadResp{}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("User-Agent", "PEB_CTRL")
	req.Header.Set("Pragma", "no-cache")
	req.Host = "sync.wanbu.com.cn"

	resp, err := client.GetHTTPClient().Do(req)
	if err != nil {
		logf("    [%s] 网络错误: %v", commond, err)
		return false, UploadResp{}
	}
	defer resp.Body.Close()
	bodyBytes, _ := io.ReadAll(resp.Body)
	bodyText := strings.TrimSpace(strings.TrimLeft(string(bodyBytes), "\xef\xbb\xbf"))
	logf("    [%s] HTTP 状态: %d, 响应: %s", commond, resp.StatusCode, bodyText)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, UploadResp{}
	}
	var hr UploadResp
	if err := json.Unmarshal([]byte(bodyText), &hr); err == nil {
		switch hr.ResultCode {
		case "0", "0000":
			return true, hr
		case "1003":
			// PedDataSync 的 1003 是"告诉客户端接下来要 recipeDownLoad"，正常
			return true, hr
		}
	}
	return true, hr
}

// 包级 log 默认丢弃，避免未走 web 入口时污染 stdout
var _ = log.Println
