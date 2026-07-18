// fuzz/cmd/capture/main.go
//
// 这个 cmd 不发任何网络请求 —— 它把"最贴近抓包"的那一份 newUploadData
// payload 写到一个文件里，供你手动 curl 上去对照服务器反应。
//
// 字段选择：
//   accessToken    = 本次 mobileToken
//   cachedata      = 0                 (数字, 跟抓包一致)
//   clientlanguage = "Chinese"         (抓包原文)
//   clientvison    = "6.5.3"           (抓包原文)
//   commond        = "newUploadData"   (与 body 内 commond 一致)
//   dayPackage     = "19"              (字符串, 抓包原文)
//   deviceType     = "TW726"           (抓包原文)
//   deviceserial   = "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx" (抓包原文)
//   hourPackage    = "431"             (抓包原文)
//   listRecipeData = 5 行 (跟抓包同日 5 个)
//   listday        = 5 行 (跟抓包同日 5 个, 但 stepNumber 全部设成 stepNumber)
//   listhour       = 5 个对象 (跟抓包同日 5 个, 但 hour7/8/9/10/11 按 stepNumber 重新分布)
//   reqservicetype = "0"               (字符串)
//   sequenceID     = 当前秒级时间戳
//
// 输出: stdout 打印整个 body, 同时写到 capture.json 供你对比
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	accessToken := flag.String("token", "PUT_MOBILE_TOKEN_HERE", "32-hex mobile accessToken from step 2")
	stepNumber := flag.Int("steps", 12000, "当天目标步数")
	flag.Parse()

	// 抓包原文: 5 天的 walkdate
	day4Before := shiftDate("20260712", -4)
	day3Before := shiftDate("20260712", -3)
	day2Before := shiftDate("20260712", -2)
	yesterday := shiftDate("20260712", -1)
	today := "20260712"
	allDates := []string{day4Before, day3Before, day2Before, yesterday, today}

	// 抓包原文: listRecipeData 每行 task1-3=1, task4-8=2 (早晨达标, 暮暮未达)
	recipeEntry := func(date string) map[string]any {
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

	// 抓包原文: listday 每行的字段顺序和精确的字段名
	// (calorieConsumed / exerciseAmount / faststepnum 都是浮点,
	//  stepNumber / stepWidth / walkTime / walkdate / weight 是基本类型,
	//  remaineffectiveSteps 是整数)
	// 抓包原始 20260708 那行: stepNumber=10987, calorieConsumed=420.9 ...
	// 我把所有 5 天的 stepNumber 改成调用者传入的值, 但保持其他比率一致
	makeDayEntry := func(date string) map[string]any {
		return map[string]any{
			"calorieConsumed":      round2(float64(*stepNumber) * 0.0281),
			"exerciseAmount":       round2(float64(*stepNumber) * 0.000473),
			"faststepnum":          int(float64(*stepNumber) * 0.475),
			"fatConsumed":          round2(float64(*stepNumber) * 0.004261),
			"goalStepNum":          10000,
			"remaineffectiveSteps": max(0, *stepNumber-10000),
			"stepNumber":           *stepNumber,
			"stepWidth":            70,
			"walkDistance":         *stepNumber * 70,
			"walkTime":             int(float64(*stepNumber) * 0.00897),
			"walkdate":             date,
			"weight":               75.0,
			"zmrule":               "5,6,7,8#3000;17,18,19,20,21,22#4000",
			"zmstatus":             "1,0",
		}
	}

	// 5 个 listhour, 每个对象 26 个 hourN + walkdate
	// 把 stepNumber 按 0.16/0.22/0.25/0.27/0.10 权重分到 7/8/9/10/11 点
	hourBuckets := splitSteps(*stepNumber)
	listhour := make([]map[string]any, 0, 5)
	for _, d := range allDates {
		listhour = append(listhour, buildHourMap(hourBuckets, d))
	}

	// 5 个 listRecipeData
	listRecipeData := make([]map[string]any, 0, 5)
	for _, d := range allDates {
		listRecipeData = append(listRecipeData, recipeEntry(d))
	}

	// 5 个 listday
	listday := make([]map[string]any, 0, 5)
	for _, d := range allDates {
		listday = append(listday, makeDayEntry(d))
	}

	body := map[string]any{
		"accessToken":    *accessToken,
		"cachedata":      0,
		"clientlanguage": "Chinese",
		"clientvison":    "6.5.3",
		"commond":        "newUploadData",
		"dayPackage":     "19",
		"deviceType":     "TW726",
		"deviceserial":   "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx",
		"hourPackage":    "431",
		"listRecipeData": listRecipeData,
		"listday":        listday,
		"listhour":       listhour,
		"reqservicetype": "0",
		"sequenceID":     fmt.Sprintf("%d", time.Now().Unix()),
	}

	bj, err := json.MarshalIndent(body, "", "  ")
	if err != nil {
		fmt.Println("序列化失败:", err)
		os.Exit(1)
	}

	if err := os.WriteFile("capture.json", bj, 0644); err != nil {
		fmt.Println("写文件失败:", err)
	}

	fmt.Println("---")
	fmt.Println(string(bj))
	fmt.Println("---")
	fmt.Println("已经写到 capture.json (注意: 这是 JSON, 不是 ReqMessageBody 包装后的 form-data)")
	fmt.Println()
	fmt.Println("下一步: 把这个 JSON 作为 ReqMessageBody 字段, 配合 commond=pcUploadData,")
	fmt.Println("POST 到 http://sync.wanbu.com.cn/WanbuDataServer_NEW/PCPedUploadFlowsService")
	fmt.Println("用 accessToken 替换:", *accessToken)
}

func shiftDate(yyyymmdd string, days int) string {
	t, err := time.Parse("20060102", yyyymmdd)
	if err != nil {
		return yyyymmdd
	}
	return t.AddDate(0, 0, days).Format("20060102")
}

func splitSteps(total int) []int {
	weights := []float64{0.16, 0.22, 0.25, 0.27, 0.10}
	out := make([]int, 5)
	assigned := 0
	for i, w := range weights {
		v := int(float64(total) * w)
		out[i] = v
		assigned += v
	}
	if diff := total - assigned; diff != 0 {
		out[0] += diff
	}
	return out
}

func buildHourMap(buckets []int, walkdate string) map[string]any {
	out := make(map[string]any, 26+1)
	hours := []int{7, 8, 9, 10, 11}
	for h := 0; h <= 25; h++ {
		key := fmt.Sprintf("hour%d", h)
		var line string
		if idx := indexOf(hours, h); idx >= 0 {
			line = hourLine(buckets[idx])
		} else {
			line = "0,0,0,0,0,0"
		}
		out[key] = line
	}
	out["walkdate"] = walkdate
	return out
}

func indexOf(s []int, v int) int {
	for i, x := range s {
		if x == v {
			return i
		}
	}
	return -1
}

func hourLine(steps int) string {
	distance := steps * 70
	fastSteps := int(float64(steps) * 0.55)
	fastDistance := fastSteps * 70
	slowSteps := steps - fastSteps
	slowDistance := slowSteps * 70
	return fmt.Sprintf("%d,%d,%d,%d,%d,%d",
		steps, distance,
		fastSteps, fastDistance,
		slowSteps, slowDistance,
	)
}

func round2(f float64) float64 {
	return float64(int(f*100+0.5)) / 100
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
