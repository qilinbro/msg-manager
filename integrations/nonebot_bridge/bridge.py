"""One bounded stdio operation. This is not a NoneBot server or plugin runner."""
import asyncio
import importlib
import io
import json
import logging
from pathlib import Path
import sys

# -I deliberately removes the script directory; add only our fixed sibling path.
_BASE = Path(__file__).resolve().parent
sys.path.insert(0, str(_BASE))
from bridge_common import BridgeError, strict_loads  # noqa: E402

MAX_REQUEST_BYTES = 64 << 10
MAX_RESPONSE_BYTES = 4 << 20
ACTIONS = {"telegram.probe": "telegram", "telegram.poll": "telegram", "feishu.send": "feishu"}


class _Discard(io.TextIOBase):
    def write(self, value):
        return len(value)

    def flush(self):
        pass


async def dispatch(request):
    if (not isinstance(request, dict) or set(request) != {"version", "action", "payload"}
            or type(request["version"]) is not int or request["version"] != 1
            or not isinstance(request["action"], str) or request["action"] not in ACTIONS
            or not isinstance(request["payload"], dict)):
        raise BridgeError("protocol_error")
    action = request["action"]
    try:
        # No init(), plugin discovery, dotenv, adapter lifecycle or default echo.
        from loguru import logger
        logger.remove()
        module = importlib.import_module(ACTIONS[action])
    except BaseException:
        raise BridgeError("unavailable", send_state="not_sent") from None
    try:
        result = await module.handle(action, request["payload"])
        if not isinstance(result, dict):
            raise TypeError("invalid result")
        return result
    except BridgeError:
        raise
    except BaseException:
        # An unexpected error after a send may have crossed the network boundary.
        raise BridgeError("send_unknown" if action == "feishu.send" else "protocol_error",
                          send_state="unknown" if action == "feishu.send" else "not_sent") from None


def main():
    wire_output = sys.stdout.buffer
    sys.stdout = _Discard()
    sys.stderr = _Discard()
    logging.disable(logging.CRITICAL)
    try:
        raw = sys.stdin.buffer.read(MAX_REQUEST_BYTES + 1)
        request = strict_loads(raw, MAX_REQUEST_BYTES)
        result = asyncio.run(dispatch(request))
        response = {"version": 1, "ok": True, "result": result}
    except BridgeError as failure:
        response = {"version": 1, "ok": False, "error": failure.as_dict()}
    except BaseException:
        response = {"version": 1, "ok": False,
                    "error": BridgeError("protocol_error", send_state="unknown").as_dict()}
    try:
        encoded = json.dumps(response, ensure_ascii=False, allow_nan=False,
                             separators=(",", ":")).encode("utf-8", "strict")
        if len(encoded) > MAX_RESPONSE_BYTES:
            raise ValueError("bounded result")
    except BaseException:
        encoded = json.dumps({"version": 1, "ok": False,
                              "error": BridgeError("protocol_error", send_state="unknown").as_dict()},
                             separators=(",", ":")).encode("ascii")
    wire_output.write(encoded + b"\n")
    wire_output.flush()


if __name__ == "__main__":
    main()
