// fuzz/cmd/fuzz/main.go
//
// 用法：
//   go run ./fuzz/cmd/fuzz -account 13369219993 -password 219993
//
// 行为：
//   1) 调 pipeline.Run 走完登录授权拿 mobileToken / pcAccessToken
//   2) 抓包字段全部来自 日志.txt 原值（deviceserial=MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx，
//      hourPackage=431, deviceversion=0bdb06, clientvison=6.5.3, weight=75）
//   3) 循环尝试不同 stepNumber + 不同 (hourPackage, dayPackage) 组合，
//      观察哪一组能让 newUploadData 返回 resultCode=0000
//   4) 即使 newUploadData 返回 1003，也照抓包原样紧跟 PedDataSync + recipeDownLoad
//      落库握手，最后 recipeDownLoad 返回 0000 才算成功
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"fkw/fuzz"
	"fkw/pipeline"
)

func main() {
	account := flag.String("account", "13369219993", "手机号")
	password := flag.String("password", "219993", "登录密码")
	flag.Parse()

	// ---------- 1) 跑一遍完整 pipeline，拿 mobileToken + pcAccessToken ----------
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	fmt.Println("[*] 步骤 A: 走完整登录授权拿 mobileToken / pcAccessToken ...")
	var mobileToken, pcToken string
	var userId uint
	{
		var sinkBuf strings.Builder
		logSink := func(s string) {
			sinkBuf.WriteString(s)
			sinkBuf.WriteString("\n")
			fmt.Println("    >", s)
		}
		res := pipeline.Run(ctx, logSink, *account, *password, 12000)
		if res.Err != "" {
			fmt.Println("[-] 登录授权失败:", res.Err)
			os.Exit(1)
		}
		mobileToken = res.MobileToken
		pcToken = res.PcToken
		userId = res.UserId
		fmt.Printf("[+] mobileToken (前 30): %s...\n", mobileToken[:30])
		fmt.Printf("[+] pcAccessToken (前 30): %s...\n", pcToken[:30])
		fmt.Printf("[+] userid=%d\n", userId)
	}

	// ---------- 2) 准备日期 ----------
	today := time.Now().Format("20060102")
	yesterday := time.Now().AddDate(0, 0, -1).Format("20060102")
	dayBefore := time.Now().AddDate(0, 0, -2).Format("20060102")

	// ---------- 3) 暴力穷举 (stepNumber, hourPackage, dayPackage) ----------
	attempts := []struct {
		stepNumber   int
		hourPackage  string
		dayPackage   string
		userid       uint
		pcTokenFmt   string
		deviceSerial string
		description  string
	}{
		{10987, "431", "19", 3936083, "", "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx", "抓包原数据"},
		{10987, "431", "19", 0, "test", "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx", "accessToken=test (空 token)"},
		{10987, "431", "19", 3936083, "", "", "deviceserial 清空"},
		{10987, "431", "19", 3936083, "928dd6f7d52a90a65616f980e4ba8a8a", "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx", "mobileToken 当 accessToken"},
		{12000, "431", "19", 3936083, "", "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx", "目标 12000"},
		{12000, "438", "19", 3936083, "", "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx", "hourPackage=438"},
		{10000, "431", "19", 3936083, "", "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx", "目标 10000"},
		{11000, "431", "19", 3936083, "", "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx", "目标 11000"},
		{12000, "431", "20", 3936083, "", "MDY4MDAwMDAxMDE3MDU0MTQ2MDAxMDAx", "dayPackage=20"},
	}

	for i, a := range attempts {
		fmt.Printf("\n[尝试 %d/%d] %s\n", i+1, len(attempts), a.description)
		// 重新计算 sequenceID 当作 correlation id
		sequenceID := fmt.Sprintf("%d", time.Now().Unix())
		status, code, body, respMsg := fuzz.TryUploadWithOverrides(ctx,
			mobileToken, today, yesterday, dayBefore, a.stepNumber,
			a.userid, a.pcTokenFmt, a.deviceSerial)
		fmt.Printf("    [newUploadData] HTTP=%d, resultCode=%q, respMsg=%q\n", status, code, respMsg)
		fmt.Printf("    Body: %s\n", preview(body, 300))

		// 不管 1003 还是 0000，都接着跑 PedDataSync + recipeDownLoad
		fmt.Println("    [*] 紧跟 PedDataSync + recipeDownLoad 握手 ...")
		fuzz.RunHandshakes(ctx, sequenceID, respMsg)
	}

	fmt.Println("\n[-] 全部尝试结束。请把 newUploadData/PedDataSync/recipeDownLoad 三段响应贴出来。")
}

func preview(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
