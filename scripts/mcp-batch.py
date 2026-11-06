#!/usr/bin/env python3
"""Submit reproducible ProofBridge experiment manifests through Streamable HTTP.

No third-party Python packages. The manifest must supply stable clientRequestId
values. Retrying the same manifest reuses its accepted submissions.
"""
import argparse
import ipaddress
import json
import math
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request


class MCP:
    def __init__(self, endpoint, token):
        parsed = urllib.parse.urlsplit(endpoint)
        loopback = parsed.hostname == "localhost"
        try:
            loopback = loopback or ipaddress.ip_address(parsed.hostname or "").is_loopback
        except ValueError:
            pass
        if parsed.scheme != "https" and not (parsed.scheme == "http" and loopback):
            raise ValueError("Use HTTPS for the Hub; HTTP is allowed only on loopback.")
        if not parsed.netloc or parsed.username or parsed.query or parsed.fragment:
            raise ValueError("Provide the copied /mcp URL without credentials or query parameters.")
        self.endpoint, self.token, self.sequence = endpoint, token, 0
        self.session_id = None
        self.protocol_version = "2025-03-26"
        reply = self.rpc("initialize", {
            "protocolVersion": self.protocol_version,
            "capabilities": {}, "clientInfo": {"name": "proofbridge-batch", "version": "1"},
        })
        self.protocol_version = reply["protocolVersion"]
        self.rpc("notifications/initialized", None, notification=True)

    def rpc(self, method, params, notification=False):
        self.sequence += 1
        payload = {"jsonrpc": "2.0", "method": method}
        if params is not None:
            payload["params"] = params
        if not notification:
            payload["id"] = self.sequence
        headers = {"Authorization": "Bearer " + self.token,
                   "Content-Type": "application/json", "Accept": "application/json, text/event-stream",
                   "MCP-Protocol-Version": self.protocol_version}
        if self.session_id:
            headers["Mcp-Session-Id"] = self.session_id
        request = urllib.request.Request(self.endpoint, json.dumps(payload).encode(), headers=headers)
        # Never forward the bearer token to a redirect target.
        opener = urllib.request.build_opener(NoRedirect())
        try:
            with opener.open(request, timeout=30) as response:
                self.session_id = response.headers.get("Mcp-Session-Id", self.session_id)
                body = response.read()
        except urllib.error.HTTPError as error:
            raise RuntimeError("MCP HTTP status " + str(error.code) + "; check VPN, URL and personal token.") from None
        if notification:
            return None
        reply = json.loads(body)
        if "error" in reply:
            raise RuntimeError("MCP protocol error: " + str(reply["error"].get("message", "unknown")))
        return reply["result"]

    def tool(self, name, arguments):
        result = self.rpc("tools/call", {"name": "proofbridge_" + name, "arguments": arguments})
        value = result.get("structuredContent")
        if value is None:
            value = json.loads(next(item["text"] for item in result["content"] if item["type"] == "text"))
        if result.get("isError"):
            error = value.get("error", {})
            raise RuntimeError(str(error.get("code", "OPERATION_FAILED")) + ": " + str(error.get("message", "")))
        return value


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--url", required=True, help="Copy the personal-center MCP URL.")
    parser.add_argument("--manifest", required=True, help="JSON file containing an array of start_run arguments.")
    parser.add_argument("--submit-only", action="store_true", help="Return accepted IDs immediately; no observation.")
    parser.add_argument("--max-wait-seconds", type=float, default=3600)
    args = parser.parse_args()
    token = os.environ.get("PROOFBRIDGE_MCP_TOKEN")
    if not token:
        raise ValueError("Set PROOFBRIDGE_MCP_TOKEN to your personal token; do not put it in the manifest.")
    if not math.isfinite(args.max_wait_seconds) or args.max_wait_seconds < 0:
        raise ValueError("--max-wait-seconds must be non-negative.")
    with open(args.manifest, encoding="utf-8") as source:
        tasks = json.load(source)
    if not isinstance(tasks, list) or not tasks:
        raise ValueError("The manifest must contain at least one task.")
    seen = set()
    for task in tasks:
        if not isinstance(task, dict) or not isinstance(task.get("clientRequestId"), str) or not task["clientRequestId"].strip():
            raise ValueError("Each task needs a stable, non-empty clientRequestId.")
        if task["clientRequestId"] in seen:
            raise ValueError("clientRequestId values must be unique within the manifest.")
        seen.add(task["clientRequestId"])
    client = MCP(args.url, token)
    report = {"schemaVersion": 1, "submissions": [], "results": [], "incomplete": []}
    # Persist accepted receipts to stdout before observation, so interruption or
    # a later submission error never hides earlier IDs.
    for task in tasks:
        receipt = client.tool("start_run", task)
        report["submissions"].append(receipt)
        print(json.dumps({"accepted": receipt}, ensure_ascii=False), flush=True)
    if not args.submit_only:
        deadline = time.monotonic() + args.max_wait_seconds
        for receipt in report["submissions"]:
            if not receipt.get("runId"):
                report["incomplete"].append(receipt)
                continue
            query = {"runId": receipt["runId"], "submissionId": receipt["submissionId"]}
            while True:
                value = client.tool("get_run", query)
                if value.get("result"):
                    report["results"].append(value)
                    break
                if time.monotonic() >= deadline:
                    report["incomplete"].append(query)
                    break
                # Observation ends here at the deadline; it never cancels work.
                time.sleep(min(1, max(0, deadline - time.monotonic())))
    print(json.dumps({"report": report}, ensure_ascii=False), flush=True)
    return 2 if report["incomplete"] else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (OSError, ValueError, RuntimeError, KeyError, StopIteration) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
