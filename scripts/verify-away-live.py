#!/usr/bin/env python3
"""Verify three close-window handoffs using a dedicated mailbox that sends to itself."""

import argparse
import getpass
import json
import os
import smtplib
import ssl
import time
import uuid
from datetime import datetime, timedelta, timezone
from email.message import EmailMessage
from email.utils import format_datetime, make_msgid
from urllib.error import HTTPError
from urllib.request import HTTPRedirectHandler, ProxyHandler, Request, build_opener

from playwright.sync_api import expect, sync_playwright

WORKBENCH = "http://127.0.0.1:1420/"
SESSIONS = "http://127.0.0.1:17890/api/v1/away-sessions"
BEIJING = timezone(timedelta(hours=8))


class NoRedirect(HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


def away_api(token, path="", method="GET"):
    request = Request(SESSIONS + path, headers={"Authorization": f"Bearer {token}"}, method=method)
    with build_opener(ProxyHandler({}), NoRedirect()).open(request, timeout=5) as response:
        return json.load(response)


def open_desk(browser, token):
    page = browser.new_page(viewport={"width": 1280, "height": 720})
    page.set_default_timeout(30_000)
    page.goto(WORKBENCH, wait_until="domcontentloaded")
    page.get_by_role("button", name="设置", exact=True).click()
    page.get_by_label("访问令牌").fill(token)
    with page.expect_response(lambda response: response.url == "http://127.0.0.1:17890/api/v1/system") as result:
        page.get_by_role("button", name="连接服务").click()
    if not result.value.ok:
        raise RuntimeError(f"工作台配对返回 HTTP {result.value.status}")
    page.get_by_role("button", name="接班", exact=True).click()
    return page


def send_to_self(args, password, subject, quote):
    message = EmailMessage()
    message["From"] = message["To"] = args.mailbox
    message["Subject"] = subject
    message["Date"] = format_datetime(datetime.now(timezone.utc))
    message["Message-ID"] = make_msgid(domain="away-desk.example")
    message.set_content(
        "Hi, this is a fictional event invitation for an Away Desk test.\n"
        f"{quote}\n"
        "Please reply to acknowledge receipt. There is no need to commit to delivery yet.\n"
    )
    with smtplib.SMTP_SSL(args.smtp_host, args.smtp_port, context=ssl.create_default_context(), timeout=20) as smtp:
        smtp.login(args.mailbox, password)
        smtp.send_message(message, from_addr=args.mailbox, to_addrs=[args.mailbox])


def wait_for_task(token, session_id, subject, quote, expected_deadline):
    end = time.monotonic() + 90
    while time.monotonic() < end:
        snapshot = away_api(token)
        session = snapshot.get("session") or {}
        if session.get("id") != session_id or session.get("status") != "running":
            raise RuntimeError("接班会话提前结束")
        tasks = [task for task in snapshot["tasks"] if task["subject"] == subject]
        if len(tasks) > 1:
            raise RuntimeError("同一封测试来信建了多个任务")
        if tasks:
            task = tasks[0]
            if task.get("draft_failed"):
                raise RuntimeError("后台备稿失败")
            if task.get("draft"):
                if (task.get("deadline") != expected_deadline or task.get("evidence") != quote
                        or len(task["requirements"]) != 2 or quote not in task["body"]):
                    raise RuntimeError("截止时间、原文证据或交付要求未通过核对")
                if not {"received", "task", "draft"} <= {event["kind"] for event in snapshot["events"]}:
                    raise RuntimeError("运行记录缺少收件、建任务或备稿")
                return task["draft"]
        time.sleep(1)
    raise RuntimeError("90 秒内未收信并完成备稿")


def run_round(chromium, args, token, password):
    subject = f"[Away Desk QA] Event materials {uuid.uuid4().hex[:10]}"
    day = (datetime.now(BEIJING) + timedelta(days=1)).date().isoformat()
    quote = f"Please send the event description and a 60-second demo video by {day} at 18:00 Beijing time."
    session_id = None
    browser = None
    try:
        browser = chromium.launch()
        page = open_desk(browser, token)
        page.get_by_role("button", name="离席授权", exact=True).click()
        page.get_by_label("关注的发件人").fill(args.mailbox)
        page.get_by_label("我确认此范围和模型处理授权").check()
        with page.expect_response(lambda response: response.url == SESSIONS and response.request.method == "POST") as result:
            page.get_by_role("button", name="开始接班", exact=True).click()
        response = result.value
        if not response.ok:
            raise RuntimeError(f"开始接班返回 HTTP {response.status}")
        session_id = response.json()["session"]["id"]
        expect(page.get_by_text("正在等候新邮件", exact=True)).to_be_visible()
        browser.close()

        started = time.monotonic()
        if args.manual_delivery:
            print(f"工作台已关闭。请在邮箱网页发给自己。\n主题  {subject}\n正文  {quote}", flush=True)
            input("发出后按回车。")
        else:
            send_to_self(args, password, subject, quote)
        draft = wait_for_task(token, session_id, subject, quote, f"{day}T18:00:00+08:00")
        elapsed = time.monotonic() - started

        browser = chromium.launch()
        page = open_desk(browser, token)
        page.get_by_role("button", name="核对截止时间 18:00 原文").click()
        expect(page.locator("mark")).to_have_text(quote)
        page.get_by_role("button", name="关闭", exact=True).click()
        page.get_by_role("button", name="审阅回信", exact=True).click()
        expect(page.get_by_label("回信草稿")).to_have_value(draft)
        return elapsed
    finally:
        if browser and browser.is_connected():
            browser.close()
        if session_id:
            away_api(token, f"/{session_id}/stop", method="POST")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mailbox", required=True, help="已在后台配置的专用测试邮箱")
    delivery = parser.add_mutually_exclusive_group(required=True)
    delivery.add_argument("--smtp-host", help="该邮箱官方提供的 SMTP TLS 主机")
    delivery.add_argument("--manual-delivery", action="store_true", help="关窗后从网页邮箱手动发给自己")
    parser.add_argument("--smtp-port", type=int, default=465)
    args = parser.parse_args()
    token = os.environ.get("SOCIALD_API_TOKEN") or getpass.getpass("本机服务访问令牌：")
    completed = 0
    try:
        snapshot = away_api(token)
        if snapshot.get("account") != args.mailbox or not snapshot.get("model"):
            raise RuntimeError("请先在后台配置同一个专用测试邮箱和模型")
        if (snapshot.get("session") or {}).get("status") == "running":
            raise RuntimeError("已有接班会话，请先在工作台结束它")
        password = ""
        if args.smtp_host:
            password = os.environ.pop("AWAY_QA_SMTP_PASSWORD", "") or getpass.getpass("SMTP 应用授权码：")
        with sync_playwright() as playwright:
            for round_number in range(1, 4):
                elapsed = run_round(playwright.chromium, args, token, password)
                completed = round_number
                print(f"第 {round_number} 轮通过 · 关窗后收信、建任务及备稿 {elapsed:.1f} 秒 · 重开复核通过", flush=True)
    except HTTPError as error:
        parser.exit(1, f"已通过 {completed}/3 轮。后台返回 HTTP {error.code}。\n")
    except RuntimeError as error:
        parser.exit(1, f"已通过 {completed}/3 轮。{error}\n")
    except Exception as error:
        parser.exit(1, f"已通过 {completed}/3 轮。测试中断（{type(error).__name__}）。\n")


if __name__ == "__main__":
    main()
