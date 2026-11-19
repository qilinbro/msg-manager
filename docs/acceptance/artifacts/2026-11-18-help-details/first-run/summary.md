# Synthetic helper-disclosure browser batch

- Result: **failed**; 6 recorded assertions; 44 findings.
- Viewports: desktop 1440×1000 and touch-enabled 390×844.
- Intercepted API transport requests: 0; forbidden business-write attempts: 0.
- All frontend bytes were fulfilled from the approved in-memory frozen dist; no request was forwarded to an application server.
- Only GET business requests and their OPTIONS preflights were permitted; all responses were synthetic. No real platform/model acceptance is claimed.
- A fresh isolated browser profile was used. Remember-device was natively unchecked and hard-asserted before synthetic connection. No personal profile, system clipboard or production credential was used.
- Source/dist/harness fingerprints unchanged: false.
- Native Enter/Space/Tab checks, computed AX names/descriptions, expanded overflow and independent unchecked consents are recorded in [report](<report.json>); protocol [trace](<cdp-trace.jsonl>) omits authorization, request bodies and fulfilled asset bodies.

## Findings

- connect-synthetic-only-1440: Scenario interrupted — Error: {"code":-32000,"message":"Object reference chain is too long"}
    at WebSocket.<anonymous> (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:542:35)
    at [nodejs.internal.kHybridDispatch] (node:internal/event_target:848:20)
    at WebSocket.dispatchEvent (node:internal/event_target:789:26)
    at fireEvent (node:internal/deps/undici/undici:14438:14)
    at #onMessage (node:internal/deps/undici/undici:15762:9)
    at Object.onMessage (node:internal/deps/undici/undici:15469:76)
    at websocketMessageReceived (node:internal/deps/undici/undici:14442:15)
    at node:internal/deps/undici/undici:15171:21
    at node:internal/deps/undici/undici:14951:11
    at afterWrite (node:internal/streams/writable:712:5)
- overview-1440: overview/工作台说明: reachable through native Tab order
- overview-1440: Scenario interrupted — AssertionError [ERR_ASSERTION]: Stop before sending activation keys to the wrong control

false !== true

    at auditDisclosures (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:197:12)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:593:77
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:593:5
- settings-1440: Scenario interrupted — Error: Timed out: synthetic model config
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:313:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async settingsBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:311:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:594:5
- milky-get-busy-success-error-1440: Scenario interrupted — Error: {"exceptionId":1,"text":"Uncaught","lineNumber":0,"columnNumber":180,"scriptId":"19","stackTrace":{"callFrames":[{"functionName":"","scriptId":"19","url":"","lineNumber":0,"columnNumber":186},{"functionName":"","scriptId":"19","url":"","lineNumber":0,"columnNumber":629}]},"exception":{"type":"object","subtype":"error","className":"Error","description":"Error: Missing/disabled 核验账号与版本\n    at <anonymous>:1:187\n    at <anonymous>:1:630","objectId":"-4566056277685542913.2.2"}}
    at evaluate (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:82:38)
    at async pointClick (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:138:17)
    at async clickText (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:151:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:326:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async settingsBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:324:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:594:5
- digest-running-1440: Scenario interrupted — Error: {"exceptionId":2,"text":"Uncaught","lineNumber":0,"columnNumber":129,"scriptId":"20","stackTrace":{"callFrames":[{"functionName":"","scriptId":"20","url":"","lineNumber":0,"columnNumber":135},{"functionName":"","scriptId":"20","url":"","lineNumber":0,"columnNumber":704}]},"exception":{"type":"object","subtype":"error","className":"Error","description":"Error: Missing/disabled section[aria-labelledby=\"digest-title\"] [aria-label=\"读取实际计划与最近摘要\"]\n    at <anonymous>:1:136\n    at <anonymous>:1:705","objectId":"-4566056277685542913.2.4"}}
    at evaluate (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:82:38)
    at async pointClick (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:138:17)
    at async click (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:149:34)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:341:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async settingsBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:339:63)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:594:5
- digest-unknown-1440: Scenario interrupted — Error: {"exceptionId":3,"text":"Uncaught","lineNumber":0,"columnNumber":129,"scriptId":"20","stackTrace":{"callFrames":[{"functionName":"","scriptId":"20","url":"","lineNumber":0,"columnNumber":135},{"functionName":"","scriptId":"20","url":"","lineNumber":0,"columnNumber":704}]},"exception":{"type":"object","subtype":"error","className":"Error","description":"Error: Missing/disabled section[aria-labelledby=\"digest-title\"] [aria-label=\"读取实际计划与最近摘要\"]\n    at <anonymous>:1:136\n    at <anonymous>:1:705","objectId":"-4566056277685542913.2.6"}}
    at evaluate (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:82:38)
    at async pointClick (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:138:17)
    at async click (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:149:34)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:341:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async settingsBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:339:63)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:594:5
- digest-failed-1440: Scenario interrupted — Error: {"exceptionId":4,"text":"Uncaught","lineNumber":0,"columnNumber":129,"scriptId":"20","stackTrace":{"callFrames":[{"functionName":"","scriptId":"20","url":"","lineNumber":0,"columnNumber":135},{"functionName":"","scriptId":"20","url":"","lineNumber":0,"columnNumber":704}]},"exception":{"type":"object","subtype":"error","className":"Error","description":"Error: Missing/disabled section[aria-labelledby=\"digest-title\"] [aria-label=\"读取实际计划与最近摘要\"]\n    at <anonymous>:1:136\n    at <anonymous>:1:705","objectId":"-4566056277685542913.2.8"}}
    at evaluate (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:82:38)
    at async pointClick (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:138:17)
    at async click (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:149:34)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:341:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async settingsBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:339:63)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:594:5
- sources-ready-and-dirty-1440: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:359:30
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:358:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- wechat-stop-get-and-late-response-1440: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:384:30
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:383:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- sources-limited-1440: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:405:34
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:404:95)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- sources-skipped-1440: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:405:34
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:404:95)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- sources-monitor-error-1440: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:405:34
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:404:95)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- sources-error-1440: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:405:34
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:404:95)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- sources-unconfigured-1440: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:405:34
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:404:95)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- inbox-ai-profile-import-1440: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:436:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async inboxPanelsBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:435:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:596:5
- feishu-ready-1440: Scenario interrupted — Error: Timed out: synthetic frozen Feishu report
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:469:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async feishuBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:466:98)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:597:5
- feishu-sent-1440: Scenario interrupted — Error: Timed out: synthetic frozen Feishu report
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:469:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async feishuBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:466:98)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:597:5
- feishu-running-1440: Scenario interrupted — Error: Timed out: synthetic frozen Feishu report
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:469:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async feishuBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:466:98)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:597:5
- feishu-failed-1440: Scenario interrupted — Error: Timed out: synthetic frozen Feishu report
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:469:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async feishuBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:466:98)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:597:5
- feishu-report-unknown-1440: Scenario interrupted — Error: Timed out: synthetic frozen Feishu report
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:469:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async feishuBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:466:98)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:597:5
- feishu-unknown-1440: Scenario interrupted — Error: Timed out: synthetic frozen Feishu report
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:469:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async feishuBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:466:98)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:597:5
- assistant-1440: Scenario interrupted — Error: Timed out: native assistant modal
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:495:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async assistantBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:493:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:598:5
- connect-synthetic-only-390: Scenario interrupted — Error: {"code":-32000,"message":"Object reference chain is too long"}
    at WebSocket.<anonymous> (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:542:35)
    at [nodejs.internal.kHybridDispatch] (node:internal/event_target:848:20)
    at WebSocket.dispatchEvent (node:internal/event_target:789:26)
    at fireEvent (node:internal/deps/undici/undici:14438:14)
    at #onMessage (node:internal/deps/undici/undici:15762:9)
    at Object.onMessage (node:internal/deps/undici/undici:15469:76)
    at websocketMessageReceived (node:internal/deps/undici/undici:14442:15)
    at node:internal/deps/undici/undici:15171:21
    at node:internal/deps/undici/undici:14951:11
    at afterWrite (node:internal/streams/writable:712:5)
- overview-390: overview/工作台说明: reachable through native Tab order
- overview-390: Scenario interrupted — AssertionError [ERR_ASSERTION]: Stop before sending activation keys to the wrong control

false !== true

    at auditDisclosures (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:197:12)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:593:77
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:593:5
- settings-390: Scenario interrupted — Error: Timed out: synthetic model config
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:313:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async settingsBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:311:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:594:5
- milky-get-busy-success-error-390: Scenario interrupted — Error: {"exceptionId":5,"text":"Uncaught","lineNumber":0,"columnNumber":180,"scriptId":"19","stackTrace":{"callFrames":[{"functionName":"","scriptId":"19","url":"","lineNumber":0,"columnNumber":186},{"functionName":"","scriptId":"19","url":"","lineNumber":0,"columnNumber":629}]},"exception":{"type":"object","subtype":"error","className":"Error","description":"Error: Missing/disabled 核验账号与版本\n    at <anonymous>:1:187\n    at <anonymous>:1:630","objectId":"-4566056277685542913.4.2"}}
    at evaluate (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:82:38)
    at async pointClick (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:138:17)
    at async clickText (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:151:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:326:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async settingsBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:324:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:594:5
- digest-running-390: Scenario interrupted — Error: {"exceptionId":6,"text":"Uncaught","lineNumber":0,"columnNumber":129,"scriptId":"20","stackTrace":{"callFrames":[{"functionName":"","scriptId":"20","url":"","lineNumber":0,"columnNumber":135},{"functionName":"","scriptId":"20","url":"","lineNumber":0,"columnNumber":704}]},"exception":{"type":"object","subtype":"error","className":"Error","description":"Error: Missing/disabled section[aria-labelledby=\"digest-title\"] [aria-label=\"读取实际计划与最近摘要\"]\n    at <anonymous>:1:136\n    at <anonymous>:1:705","objectId":"-4566056277685542913.4.4"}}
    at evaluate (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:82:38)
    at async pointClick (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:138:17)
    at async click (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:149:34)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:341:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async settingsBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:339:63)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:594:5
- digest-unknown-390: Scenario interrupted — Error: {"exceptionId":7,"text":"Uncaught","lineNumber":0,"columnNumber":129,"scriptId":"20","stackTrace":{"callFrames":[{"functionName":"","scriptId":"20","url":"","lineNumber":0,"columnNumber":135},{"functionName":"","scriptId":"20","url":"","lineNumber":0,"columnNumber":704}]},"exception":{"type":"object","subtype":"error","className":"Error","description":"Error: Missing/disabled section[aria-labelledby=\"digest-title\"] [aria-label=\"读取实际计划与最近摘要\"]\n    at <anonymous>:1:136\n    at <anonymous>:1:705","objectId":"-4566056277685542913.4.6"}}
    at evaluate (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:82:38)
    at async pointClick (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:138:17)
    at async click (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:149:34)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:341:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async settingsBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:339:63)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:594:5
- digest-failed-390: Scenario interrupted — Error: {"exceptionId":8,"text":"Uncaught","lineNumber":0,"columnNumber":129,"scriptId":"20","stackTrace":{"callFrames":[{"functionName":"","scriptId":"20","url":"","lineNumber":0,"columnNumber":135},{"functionName":"","scriptId":"20","url":"","lineNumber":0,"columnNumber":704}]},"exception":{"type":"object","subtype":"error","className":"Error","description":"Error: Missing/disabled section[aria-labelledby=\"digest-title\"] [aria-label=\"读取实际计划与最近摘要\"]\n    at <anonymous>:1:136\n    at <anonymous>:1:705","objectId":"-4566056277685542913.4.8"}}
    at evaluate (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:82:38)
    at async pointClick (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:138:17)
    at async click (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:149:34)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:341:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async settingsBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:339:63)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:594:5
- sources-ready-and-dirty-390: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:359:30
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:358:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- wechat-stop-get-and-late-response-390: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:384:30
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:383:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- sources-limited-390: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:405:34
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:404:95)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- sources-skipped-390: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:405:34
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:404:95)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- sources-monitor-error-390: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:405:34
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:404:95)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- sources-error-390: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:405:34
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:404:95)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- sources-unconfigured-390: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:405:34
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async sourcesBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:404:95)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:595:5
- inbox-ai-profile-import-390: Scenario interrupted — Error: Timed out: synthetic inbox
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async sourcePage (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:352:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:436:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async inboxPanelsBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:435:3)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:596:5
- feishu-ready-390: Scenario interrupted — Error: Timed out: synthetic frozen Feishu report
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:469:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async feishuBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:466:98)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:597:5
- feishu-sent-390: Scenario interrupted — Error: Timed out: synthetic frozen Feishu report
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:469:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async feishuBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:466:98)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:597:5
- feishu-running-390: Scenario interrupted — Error: Timed out: synthetic frozen Feishu report
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:469:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async feishuBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:466:98)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:597:5
- feishu-failed-390: Scenario interrupted — Error: Timed out: synthetic frozen Feishu report
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:469:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async feishuBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:466:98)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:597:5
- feishu-report-unknown-390: Scenario interrupted — Error: Timed out: synthetic frozen Feishu report
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:469:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async feishuBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:466:98)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:597:5
- Batch error: Error: Timed out: synthetic frozen Feishu report
    at until (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:93:9)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:469:5
    at async scenario (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:113:9)
    at async feishuBatch (file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:466:98)
    at async file:///D:/msg_manager/.runtime/main-push-20261116/repo/.runtime/T08-helper-details-20261118/synthetic-browser.mjs:597:5

## Screenshots

- [failure-connect-synthetic-only-1440.png](<failure-connect-synthetic-only-1440.png>)
- [failure-overview-1440.png](<failure-overview-1440.png>)
- [failure-settings-1440.png](<failure-settings-1440.png>)
- [failure-milky-get-busy-success-error-1440.png](<failure-milky-get-busy-success-error-1440.png>)
- [failure-digest-running-1440.png](<failure-digest-running-1440.png>)
- [failure-digest-unknown-1440.png](<failure-digest-unknown-1440.png>)
- [failure-digest-failed-1440.png](<failure-digest-failed-1440.png>)
- [failure-sources-ready-and-dirty-1440.png](<failure-sources-ready-and-dirty-1440.png>)
- [failure-wechat-stop-get-and-late-response-1440.png](<failure-wechat-stop-get-and-late-response-1440.png>)
- [failure-sources-limited-1440.png](<failure-sources-limited-1440.png>)
- [failure-sources-skipped-1440.png](<failure-sources-skipped-1440.png>)
- [failure-sources-monitor-error-1440.png](<failure-sources-monitor-error-1440.png>)
- [failure-sources-error-1440.png](<failure-sources-error-1440.png>)
- [failure-sources-unconfigured-1440.png](<failure-sources-unconfigured-1440.png>)
- [failure-inbox-ai-profile-import-1440.png](<failure-inbox-ai-profile-import-1440.png>)
- [failure-feishu-ready-1440.png](<failure-feishu-ready-1440.png>)
- [failure-feishu-sent-1440.png](<failure-feishu-sent-1440.png>)
- [failure-feishu-running-1440.png](<failure-feishu-running-1440.png>)
- [failure-feishu-failed-1440.png](<failure-feishu-failed-1440.png>)
- [failure-feishu-report-unknown-1440.png](<failure-feishu-report-unknown-1440.png>)
- [failure-feishu-unknown-1440.png](<failure-feishu-unknown-1440.png>)
- [failure-assistant-1440.png](<failure-assistant-1440.png>)
- [failure-connect-synthetic-only-390.png](<failure-connect-synthetic-only-390.png>)
- [failure-overview-390.png](<failure-overview-390.png>)
- [failure-settings-390.png](<failure-settings-390.png>)
- [failure-milky-get-busy-success-error-390.png](<failure-milky-get-busy-success-error-390.png>)
- [failure-digest-running-390.png](<failure-digest-running-390.png>)
- [failure-digest-unknown-390.png](<failure-digest-unknown-390.png>)
- [failure-digest-failed-390.png](<failure-digest-failed-390.png>)
- [failure-sources-ready-and-dirty-390.png](<failure-sources-ready-and-dirty-390.png>)
- [failure-wechat-stop-get-and-late-response-390.png](<failure-wechat-stop-get-and-late-response-390.png>)
- [failure-sources-limited-390.png](<failure-sources-limited-390.png>)
- [failure-sources-skipped-390.png](<failure-sources-skipped-390.png>)
- [failure-sources-monitor-error-390.png](<failure-sources-monitor-error-390.png>)
- [failure-sources-error-390.png](<failure-sources-error-390.png>)
- [failure-sources-unconfigured-390.png](<failure-sources-unconfigured-390.png>)
- [failure-inbox-ai-profile-import-390.png](<failure-inbox-ai-profile-import-390.png>)
- [failure-feishu-ready-390.png](<failure-feishu-ready-390.png>)
- [failure-feishu-sent-390.png](<failure-feishu-sent-390.png>)
- [failure-feishu-running-390.png](<failure-feishu-running-390.png>)
- [failure-feishu-failed-390.png](<failure-feishu-failed-390.png>)
- [failure-feishu-report-unknown-390.png](<failure-feishu-report-unknown-390.png>)
