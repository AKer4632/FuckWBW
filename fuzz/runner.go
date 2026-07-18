// fuzz/runner.go
//
// TryUpload  调一次 newUploadData，把响应解析成 (httpStatus, resultCode, rawBody)
// TryUploadWithOverrides  同上，但允许覆盖 accessToken / deviceserial
// RunHandshakes 跑 PedDataSync + recipeDownLoad 落库握手
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

// TryUpload 调一次 newUploadData 并打印 (httpStatus, resultCode, body 摘要)
// mobileToken 是手机端登录返回的 32-hex accessToken。
func TryUpload(ctx context.Context, mobileToken, today, yesterday, dayBefore string, stepNumber int) (int, string, string) {
	status, code, body, err := PostUpload(ctx, mobileToken, today, yesterday, dayBefore, stepNumber)
	if err != nil {
		return status, code, "网络错误: " + err.Error()
	}
	return status, code, body
}

// TryUploadWithOverrides 让调用者覆盖 accessToken / deviceserial，
// 用来定位"哪个字段让服务器进入 1003 分支"。
func TryUploadWithOverrides(ctx context.Context, mobileToken, today, yesterday, dayBefore string, stepNumber int, _ uint, accessTokenOverride, deviceSerialOverride string) (int, string, string, string) {
	body, err := postUploadRaw(ctx, mobileToken, today, yesterday, dayBefore, stepNumber, accessTokenOverride, deviceSerialOverride)
	if err != nil {
		return 0, "", "网络错误: " + err.Error(), ""
	}
	var ur struct {
		ResultCode string `json:"resultCode"`
		RespMsg    string `json:"respMsg"`
	}
	_ = json.Unmarshal([]byte(body), &ur)
	return 200, ur.ResultCode, body, ur.RespMsg
}

// postUploadRaw 是 PostUpload 的可覆盖版本，accessToken/deviceserial 都允许外部指定。
func postUploadRaw(ctx context.Context, mobileToken, today, yesterday, dayBefore string, stepNumber int, accessTokenOverride, deviceSerialOverride string) (string, error) {
	const syncBase = "http://sync.wanbu.com.cn"
	accessToken := mobileToken
	if accessTokenOverride == "test" {
		accessToken = "test"
	} else if accessTokenOverride != "" {
		accessToken = accessTokenOverride
	}
	devSerial := "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx"
	if deviceSerialOverride == "" {
		// 显式空字符串 -> 模拟"去掉这个字段"
		devSerial = ""
	}
	payload := BuildUploadPayloadRaw(accessToken, today, yesterday, dayBefore, stepNumber, devSerial)
	pj, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	form := url.Values{}
	form.Set("commond", "pcUploadData")
	form.Set("ReqMessageBody", string(pj))

	req, err := http.NewRequestWithContext(ctx, "POST",
		syncBase+"/WanbuDataServer_NEW/PCPedUploadFlowsService",
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=utf-8")
	req.Header.Set("User-Agent", "okhttp/4.11.0")
	req.Header.Set("Pragma", "no-cache")
	req.Host = "sync.wanbu.com.cn"

	up := libs.NewClient(syncBase, 15*time.Second)
	resp, err := up.GetHTTPClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	bb, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(strings.TrimLeft(string(bb), "\xef\xbb\xbf")), nil
}

// BuildUploadPayloadRaw 是 BuildUploadPayload 的可覆盖版本。
func BuildUploadPayloadRaw(accessToken, today, yesterday, dayBefore string, stepNumber int, deviceSerial string) map[string]any {
	hourBuckets := splitStepsIntoHours(stepNumber, []int{7, 8, 9, 10, 11})
	hourStrings := make(map[string]string, 5)
	for i, h := range []int{7, 8, 9, 10, 11} {
		hourStrings[fmt.Sprintf("hour%d", h)] = formatHourLine(hourBuckets[i], 70)
	}

	recipeEntry := func(date string) map[string]any {
		return map[string]any{
			"recipenumber": 9999,
			"task1state":   1, // 早晨完成
			"task2state":   1,
			"task3state":   1,
			"task4state":   2, // 暮暮未完成 — 与抓包一致
			"task5state":   2,
			"task6state":   2,
			"task7state":   2,
			"task8state":   2,
			"walkdate":     date,
		}
	}

	day4Before := shiftDate(today, -4)
	day3Before := shiftDate(today, -3)
	allDates := []string{day4Before, day3Before, dayBefore, yesterday, today}

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
			"zmstatus":             "1,0",
		}
	}
	listday := make([]map[string]any, 0, 5)
	for _, d := range allDates {
		listday = append(listday, makeDayEntry(d))
	}
	listhour := make([]map[string]any, 0, 5)
	for _, d := range allDates {
		listhour = append(listhour, withAllHours(hourStrings, d))
	}
	listRecipeData := make([]map[string]any, 0, 5)
	for _, d := range allDates {
		listRecipeData = append(listRecipeData, recipeEntry(d))
	}

	m := map[string]any{
		"accessToken":    accessToken,
		"cachedata":      0,
		"clientlanguage": "Chinese",
		"clientvison":    "6.5.3",
		"commond":        "newUploadData",
		"dayPackage":     "19",
		"deviceType":     "TW726",
		"hourPackage":    "431",
		"listRecipeData": listRecipeData,
		"listday":        listday,
		"listhour":       listhour,
		"reqservicetype": "0",
		"sequenceID":     fmt.Sprintf("%d", time.Now().Unix()),
	}
	if deviceSerial != "" {
		m["deviceserial"] = deviceSerial
	}
	return m
}

// RunHandshakes 跑 PedDataSync + recipeDownLoad 落库握手
// sequenceID 是 newUploadData 时用的那个时间戳
// respMsgToken 是 newUploadData 响应里的 respMsg（暂未在 PedDataSync 中透传）
func RunHandshakes(ctx context.Context, sequenceID, respMsgToken string) {
	syncBody := PedDataSyncPayload(sequenceID, respMsgToken)
	status, code, body, _ := PostCommond(ctx, "PedDataSync", syncBody)
	fmt.Printf("    [PedDataSync] HTTP=%d resultCode=%q body=%s\n", status, code, preview(body, 200))

	recipeBody := RecipeDownLoadPayload(sequenceID)
	status, code, body, _ = PostCommond(ctx, "recipeDownLoad", recipeBody)
	fmt.Printf("    [recipeDownLoad] HTTP=%d resultCode=%q body=%s\n", status, code, preview(body, 200))
}

func preview(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
