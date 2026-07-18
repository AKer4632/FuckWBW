import json
import time

import requests

current_timestamp = str(int(time.time()))

payload_data = {
    "accessToken": "0c250d2e43cf3af5b0a9b9a9d1cc94aa",
    "cachedata": 0,
    "clientlanguage": "Chinese",
    "clientvison": "6.5.3",
    "commond": "newUploadData",
    "dayPackage": "19",
    "deviceType": "TW726",
    "deviceserial": "MDY4MDAwMDAxMDE3MjQ1MTI5MDAzMTA4",
    "hourPackage": "438",
    "listRecipeData": [
        {
            "recipenumber": 9999,
            "task1state": 1,
            "task2state": 1,
            "task3state": 1,
            "task4state": 2,
            "task5state": 2,
            "task6state": 2,
            "task7state": 2,
            "task8state": 2,
            "walkdate": "20260710",
        },
        {
            "recipenumber": 9999,
            "task1state": 1,
            "task2state": 1,
            "task3state": 1,
            "task4state": 2,
            "task5state": 2,
            "task6state": 2,
            "task7state": 2,
            "task8state": 2,
            "walkdate": "20260711",
        },
        {
            "recipenumber": 9999,
            "task1state": 1,
            "task2state": 1,
            "task3state": 1,
            "task4state": 2,
            "task5state": 2,
            "task6state": 2,
            "task7state": 2,
            "task8state": 2,
            "walkdate": "20260712",
        },
    ],
    "listday": [
        {
            "calorieConsumed": 480.5,
            "exerciseAmount": 8.5,
            "faststepnum": 10000,
            "fatConsumed": 68.2,
            "goalStepNum": 10000,
            "remaineffectiveSteps": 7050,
            "stepNumber": 17070,  # 保持 17050 总步数
            "stepWidth": 70,
            "walkDistance": 1193500,
            "walkTime": 165,
            "walkdate": "20260712",
            "weight": 54,
            "zmrule": "5,6,7,8#3000;17,18,19,20,21,22#4000",
            "zmstatus": "1,1",  # 朝朝达标，暮暮未达标
        }
    ],
    "listhour": [
        {
            "hour0": "0,0,0,0,0,0",
            "hour1": "0,0,0,0,0,0",
            "hour2": "0,0,0,0,0,0",
            "hour3": "0,0,0,0,0,0",
            "hour4": "0,0,0,0,0,0",
            "hour5": "0,0,0,0,0,0",
            "hour6": "0,0,0,0,0,0",
            # ── 强行集中到朝朝的区间（7点和8点） ──
            "hour7": "8000,560000,5000,350000,3000,210000",
            "hour8": "9050,633500,5000,350000,4050,283500",
            # ── 其他已发生的小时清空 ──
            "hour9": "0,0,0,0,0,0",
            "hour10": "0,0,0,0,0,0",
            "hour11": "0,0,0,0,0,0",
            "hour12": "0,0,0,0,0,0",
            "hour13": "0,0,0,0,0,0",
            "hour14": "0,0,0,0,0,0",
            "hour15": "0,0,0,0,0,0",
            "hour16": "0,0,0,0,0,0",
            "hour17": "0,0,0,0,0,0",
            "hour18": "0,0,0,0,0,0",
            "hour19": "0,0,0,0,0,0",
            "hour20": "0,0,0,0,0,0",
            "hour21": "0,0,0,0,0,0",
            "hour22": "0,0,0,0,0,0",
            "hour23": "0,0,0,0,0,0",
            "hour24": "0,0,0,0,0,0",
            "hour25": "0,0,0,0,0,0",
            "walkdate": "20260712",
        }
    ],
    "reqservicetype": "0",
    "sequenceID": current_timestamp,
}

headers = {
    "Content-Type": "application/x-www-form-urlencoded;charset=utf-8",
    "User-Agent": "PEB_CTRL",
    "Pragma": "no-cache",
    "Host": "sync.wanbu.com.cn",
}

form_data = {
    "commond": "pcUploadData",
    "ReqMessageBody": json.dumps(payload_data, separators=(",", ":")),
}

url = "http://sync.wanbu.com.cn/WanbuDataServer_NEW/PCPedUploadFlowsService"
response = requests.post(url, headers=headers, data=form_data)

print("[*] 响应状态码:", response.status_code)
print("[*] 响应体:", response.text)
