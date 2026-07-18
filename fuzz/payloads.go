// Package fuzz 是一组"暴力试错"实验体 —— 把 pipeline.Run 拆成可单步复用的子函数，
// 然后在 fuzz/cmd 里循环改变可疑字段（deviceserial / hourPackage / dayPackage /
// clientvison / weight / 步数），看哪一种组合能拿到 resultCode=0000。
//
// 这个文件只提供可复用的子函数 + 一些载荷模板，不直接跑。
package fuzz

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"fkw/libs"
)

// 抓包原文（来自 日志.txt）：
//   accessToken    = 21dd2d1e5745272d4d630f81ea49a08e
//   deviceserial   = MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx   (decode: 0480000017054146001001)
//   deviceversion  = 0bdb06
//   hourPackage    = 431
//   dayPackage     = 19
//   clientvison    = 6.5.3
//   deviceType     = TW726
//   weight         = 75
//   stepWidth      = 70
//   zmrule         = 5,6,7,8#3000;17,18,19,20,21,22#4000
//
// 反过来用：之前 pipeline.go 里 deviceserial=MDY4MDAwMDAxMDE3MjQ1MTI5MDAzMTA4
// 来自 main.py 旧抓包，跟当前真实设备的 serial 根本不是一个东西。

// BuildUploadPayload 重新组装 newUploadData 用的完整载荷。
//
// 【重要】抓包（@日志.txt）显示：
//  1) body 里的 "accessToken" 字段填的是【手机端登录时返回的 accessToken】
//     (32 位十六进制)，不是 PC 端轮询拿到的 assesstoken
//  2) listday 必须是【5 天】(today-4 .. today)，每天一行
//  3) listhour 必须是【5 个对象】(每天一个)，每个对象 26 个 hourN
//  4) listRecipeData 必须是【5 天】(抓包里也是 5 个)
//  5) zmstatus 应为 "1,0"（朝朝达标、暮暮未达标）—— 发送 "1,1"
//     会让服务器进入 malformed 分支、返回固定的 placeholder respMsg
//
// 之前 pipeline.Run 把 pcAssesstoken 填进了 accessToken，listday 只有 1
// 行，zmstatus 写成了 "1,1"，全部 3 个原因都导致服务器拒绝并返回
// resultCode=1003 + 那个固定的 "1cu6xdDFz6LS0bn9xtqjrMfr1tjQwrXHwrwh" 占位
// 符。
func BuildUploadPayload(mobileToken, today, yesterday, dayBefore string, stepNumber int) map[string]any {
	hourBuckets := splitStepsIntoHours(stepNumber, []int{7, 8, 9, 10, 11})
	hourStrings := make(map[string]string, 5)
	for i, h := range []int{7, 8, 9, 10, 11} {
		hourStrings[fmt.Sprintf("hour%d", h)] = formatHourLine(hourBuckets[i], 70)
	}

	// 早晨任务 (task1-3) 完成 + 暮暮任务 (task4-8) 未完成
	//
	// 关键: 抓包 (日志.txt) 显示真实客户端的 task1-3 = 1 (早晨达标)、
	// task4-8 = 2 (暮暮未达标) — 即使步数已经超过 10000 也会这样发，
	// 因为暮暮任务是 17-22 点的硬时段，强行把它的状态写成 1 但 listhour
	// 里 17-22 点都是 0 的话，服务器会检测到"状态和小时数据不一致"，
	// 直接走 1003 占位符分支不落库。
	recipeEntry := func(date string) map[string]any {
		return map[string]any{
			"recipenumber": 9999,
			"task1state":   1, // 早晨任务完成 (5-9 点, 3000 步)
			"task2state":   1, // 早晨任务
			"task3state":   1, // 早晨任务
			"task4state":   2, // 暮暮任务未完成 (17-22 点, 4000 步) — 与抓包一致
			"task5state":   2,
			"task6state":   2,
			"task7state":   2,
			"task8state":   2,
			"walkdate":     date,
		}
	}

	// 5 个日期：今天-4, 今天-3, 今天-2, 昨天, 今天
	// 用 today / yesterday / dayBefore 三个已知日期，向前再补 2 天
	day4Before := shiftDate(today, -4)
	day3Before := shiftDate(today, -3)
	allDates := []string{day4Before, day3Before, dayBefore, yesterday, today}

	// 每天的 listday（步数/距离/热量 等都按 stepNumber 算）
	makeDayEntry := func(date string) map[string]any {
		return map[string]any{
			"calorieConsumed":      round2(float64(stepNumber) * 0.0281),
			"exerciseAmount":       round2(float64(stepNumber) * 0.000473),
			"faststepnum":          int(float64(stepNumber) * 0.475),
			"fatConsumed":          round2(float64(stepNumber) * 0.004261),
			"goalStepNum":          10000,
			"remaineffectiveSteps": max(0, stepNumber-10000),
			"stepNumber":           stepNumber,
			"stepWidth":            70,
			"walkDistance":         stepNumber * 70,
			"walkTime":             int(float64(stepNumber) * 0.00897),
			"walkdate":             date,
			"weight":               75.0,
			"zmrule":               "5,6,7,8#3000;17,18,19,20,21,22#4000",
			"zmstatus":             "1,0", // 朝朝达标、暮暮未达标，与抓包一致
		}
	}

	listday := make([]map[string]any, 0, 5)
	for _, d := range allDates {
		listday = append(listday, makeDayEntry(d))
	}

	// 5 个 listhour：每个对象对应一天，walkdate 不同
	listhour := make([]map[string]any, 0, 5)
	for _, d := range allDates {
		listhour = append(listhour, withAllHours(hourStrings, d))
	}

	// 5 个 listRecipeData
	listRecipeData := make([]map[string]any, 0, 5)
	for _, d := range allDates {
		listRecipeData = append(listRecipeData, recipeEntry(d))
	}

	return map[string]any{
		"accessToken":    mobileToken,
		"cachedata":      0,
		"clientlanguage": "Chinese",
		"clientvison":    "6.5.3",
		"commond":        "newUploadData",
		"dayPackage":     "19",
		"deviceType":     "TW726",
		"deviceserial":   "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx", // 抓包原文
		"hourPackage":    "431",                                // 抓包原文
		"listRecipeData": listRecipeData,
		"listday":        listday,
		"listhour":       listhour,
		"reqservicetype": "0",
		"sequenceID":     fmt.Sprintf("%d", time.Now().Unix()), // 客户端秒级时间戳
	}
}

// shiftDate 把 "20260712" 这种 YYYYMMDD 字符串往前 / 往后推 n 天，返回新的 YYYYMMDD。
func shiftDate(yyyymmdd string, days int) string {
	t, err := time.Parse("20060102", yyyymmdd)
	if err != nil {
		return yyyymmdd
	}
	return t.AddDate(0, 0, days).Format("20060102")
}

// PedDataSyncPayload 组装步骤 2（PedDataSync）握手用的载荷。
// sequenceID 是 newUploadData 时用的那个时间戳（保持一致，服务器用它把
// "刚才那次上传"和"这次握手"绑在一起）。
func PedDataSyncPayload(sequenceID, respMsgToken string) map[string]any {
	// 抓包原文里 PedDataSync 的 ReqMessageBody 字段列表是固定的，没有
	// respMsg 这种"握手 token"字段；token 是在 newUploadData 响应里
	// 的 respMsg 字段里给到客户端的，但客户端在 PedDataSync 这一步
	// 只是回传 sequenceID 让服务器知道要 commit 哪一次上传。
	// 所以我们暂时不带 respMsg，留给后续的 fuzz 试探。
	_ = respMsgToken
	return map[string]any{
		"accessToken":    "",
		"clientname":     "PCClient",
		"clientvison":    "6.5.3",
		"commond":        "PedDataSync",
		"deviceType":     "TW726",
		"deviceserial":   "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx",
		"deviceversion":  "0bdb06",
		"eveningBegin":   "17",
		"eveningEnd":     "22",
		"eveningStepNum": "4000",
		"goalStepNum":    10000,
		"morningBegin":   "5",
		"morningEnd":     "8",
		"morningStepNum": "3000",
		"pedmodel":       "-1",
		"reqservicetype": 0,
		"sequenceID":     sequenceID,
		"stepwith":       70,
		"timezone":       "0",
		"weight":         75.0,
	}
}

// RecipeDownLoadPayload 组装步骤 3（recipeDownLoad）握手用的载荷。
func RecipeDownLoadPayload(sequenceID string) map[string]any {
	return map[string]any{
		"accessToken":    "",
		"clientvison":    "6.5.3",
		"commond":        "recipeDownLoad",
		"deviceType":     "TW726",
		"deviceserial":   "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx",
		"reqservicetype": 0,
		"sequenceID":     sequenceID,
	}
}

// PostUpload 发 newUploadData 一次，返回 (httpStatus, parsedResultCode, rawBody)。
// mobileToken 是手机端登录返回的 32-hex accessToken (不是 PC 端轮询的 base64 串)。
//
// 抓包 (日志.txt) 显示真实客户端的 User-Agent 是 okhttp/4.11.0 而不是
// PEB_CTRL — 早期用 PEB_CTRL 是因为我们走的是 PC 端 init/poll 那条线，
// 但 sync.wanbu.com.cn 的上传接口其实和移动端共用，会校验 UA 走
// "mobile client" 路径；如果用 PC UA 会被服务器直接返回占位符 1003。
func PostUpload(ctx context.Context, mobileToken, today, yesterday, dayBefore string, stepNumber int) (int, string, string, error) {
	const syncBase = "http://sync.wanbu.com.cn"
	payload := BuildUploadPayload(mobileToken, today, yesterday, dayBefore, stepNumber)
	pj, err := json.Marshal(payload)
	if err != nil {
		return 0, "", "", err
	}
	form := url.Values{}
	form.Set("commond", "pcUploadData")
	form.Set("ReqMessageBody", string(pj))

	req, err := http.NewRequestWithContext(ctx, "POST",
		syncBase+"/WanbuDataServer_NEW/PCPedUploadFlowsService",
		strings.NewReader(form.Encode()))
	if err != nil {
		return 0, "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("User-Agent", "okhttp/4.11.0")
	req.Header.Set("Pragma", "no-cache")
	req.Host = "sync.wanbu.com.cn"

	up := libs.NewClient(syncBase, 15*time.Second)
	resp, err := up.GetHTTPClient().Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	bodyStr := strings.TrimSpace(strings.TrimLeft(string(body), "\xef\xbb\xbf"))

	var ur struct {
		ResultCode string `json:"resultCode"`
	}
	_ = json.Unmarshal([]byte(bodyStr), &ur)
	return resp.StatusCode, ur.ResultCode, bodyStr, nil
}

// PostCommond 是 PedDataSync / recipeDownLoad 用的统一 POST 工具。
func PostCommond(ctx context.Context, commond string, body map[string]any) (int, string, string, error) {
	const syncBase = "http://sync.wanbu.com.cn"
	bj, err := json.Marshal(body)
	if err != nil {
		return 0, "", "", err
	}
	form := url.Values{}
	form.Set("commond", commond)
	form.Set("ReqMessageBody", string(bj))

	req, err := http.NewRequestWithContext(ctx, "POST",
		syncBase+"/WanbuDataServer_NEW/PCPedUploadFlowsService",
		strings.NewReader(form.Encode()))
	if err != nil {
		return 0, "", "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("User-Agent", "okhttp/4.11.0")
	req.Header.Set("Pragma", "no-cache")
	req.Host = "sync.wanbu.com.cn"

	up := libs.NewClient(syncBase, 15*time.Second)
	resp, err := up.GetHTTPClient().Do(req)
	if err != nil {
		return 0, "", "", err
	}
	defer resp.Body.Close()
	bb, _ := io.ReadAll(resp.Body)
	bodyStr := strings.TrimSpace(strings.TrimLeft(string(bb), "\xef\xbb\xbf"))

	var ur struct {
		ResultCode string `json:"resultCode"`
		RespMsg    string `json:"respMsg"`
	}
	_ = json.Unmarshal([]byte(bodyStr), &ur)
	return resp.StatusCode, ur.ResultCode, bodyStr, nil
}

// ---------- helpers（跟 pipeline.go 里同款，但放在 fuzz 包里方便独立 go test） ----------

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

func formatHourLine(steps, stepWidthCM int) string {
	distance := steps * stepWidthCM
	fastSteps := int(float64(steps) * 0.55)
	fastDistance := fastSteps * stepWidthCM
	slowSteps := steps - fastSteps
	slowDistance := slowSteps * stepWidthCM
	return fmt.Sprintf("%d,%d,%d,%d,%d,%d",
		steps, distance,
		fastSteps, fastDistance,
		slowSteps, slowDistance,
	)
}

func withAllHours(filled map[string]string, walkdate string) map[string]any {
	out := make(map[string]any, 26+1)
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
