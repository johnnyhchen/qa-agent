"""
Shared utilities for ai-computer-use adapters (macOS and iOS).

Provides:
- API key checking (Anthropic or Google)
- CLI availability checks
- Unified prompt builder with UI context support
- Structured JSON verdict parsing
- Evidence collection
- CLIOutput writer
- ai-computer-use command builder
- Outcome parsing (structured + fallback)
"""

import json
import os
import re
import subprocess


def check_api_key(provider="gemini"):
    """Check if the appropriate API key is set for the given provider."""
    if provider == "anthropic":
        return bool(os.environ.get("ANTHROPIC_API_KEY"))
    if provider == "gemini":
        return bool(os.environ.get("GEMINI_API_KEY"))
    # Check both as fallback
    return bool(os.environ.get("ANTHROPIC_API_KEY") or os.environ.get("GEMINI_API_KEY"))


def check_cli_available(name="ai-computer-use"):
    """Check if a CLI tool is available in PATH."""
    try:
        result = subprocess.run(
            [name, "--help"],
            capture_output=True, text=True, timeout=10,
        )
        return result.returncode == 0
    except (FileNotFoundError, subprocess.TimeoutExpired):
        return False


def build_prompt(app_name, steps, assertions, ui_context=None, surface="macos"):
    """Build a structured prompt for ai-computer-use.

    Args:
        app_name: Display name of the application.
        steps: List of step strings.
        assertions: List of assertion strings.
        ui_context: Optional dict with 'layout', 'tips', 'accessibility_ids'.
        surface: 'macos' or 'ios' — adjusts preamble.
    """
    parts = []

    if surface == "ios":
        parts.append(
            f"The iOS Simulator window is showing the '{app_name}' app. "
            "Interact with the Simulator window to test the app."
        )
    else:
        parts.append(f"You are testing the application '{app_name}'.")

    parts.append("")

    # UI context block
    if ui_context:
        layout = ui_context.get("layout", "")
        tips = ui_context.get("tips", [])
        aids = ui_context.get("accessibility_ids", {})

        if layout:
            parts.append("=== UI LAYOUT ===")
            parts.append(layout)
            parts.append("")

        if tips:
            parts.append("Interaction tips:")
            for tip in tips:
                parts.append(f"  - {tip}")
            parts.append("")

        if aids:
            parts.append("Accessibility identifiers:")
            for aid_key, aid_desc in aids.items():
                parts.append(f'  - "{aid_key}": {aid_desc}')
            parts.append("")

    parts.append("Perform the following steps in order:")
    for i, step in enumerate(steps, 1):
        parts.append(f"  {i}. {step}")
    parts.append("")
    parts.append("After completing all steps, verify each of the following assertions:")
    for i, assertion in enumerate(assertions, 1):
        parts.append(f"  {i}. {assertion}")
    parts.append("")
    parts.append(
        "For each assertion, carefully check the current screen state. "
        "If ALL assertions pass, report success. "
        "If ANY assertion fails, describe which assertion failed and what you observed instead."
    )

    # Structured verdict suffix
    parts.append("")
    parts.append(
        'After verifying all assertions, output your final verdict as a JSON block:\n'
        '```json\n'
        '{"verdict": "PASS" or "FAIL", "assertions": [{"id": 1, "passed": true/false, "observed": "what you saw"}]}\n'
        '```'
    )

    return "\n".join(parts)


def parse_structured_verdict(stdout):
    """Extract a structured JSON verdict from agent stdout.

    Returns (verdict_str, verdict_data) or (None, None).
    verdict_str is "pass" or "fail" (lowercase).
    """
    if not stdout:
        return None, None

    # Try fenced JSON block first: ```json ... ```
    match = re.search(r'```json\s*\n?(.*?)\n?\s*```', stdout, re.DOTALL)
    if match:
        try:
            data = json.loads(match.group(1).strip())
            verdict = data.get("verdict", "").upper()
            if verdict in ("PASS", "FAIL"):
                return verdict.lower(), data
        except (json.JSONDecodeError, AttributeError):
            pass

    # Fallback: bare JSON with verdict key
    match = re.search(r'\{[^{}]*"verdict"\s*:\s*"(PASS|FAIL)"[^{}]*\}', stdout, re.IGNORECASE)
    if match:
        try:
            data = json.loads(match.group(0))
            verdict = data.get("verdict", "").upper()
            if verdict in ("PASS", "FAIL"):
                return verdict.lower(), data
        except (json.JSONDecodeError, AttributeError):
            # Just use the regex capture
            return match.group(1).lower(), None

    return None, None


def collect_evidence(artifact_dir, screenshot_dir, extra_files=None):
    """Collect all evidence files from artifact and screenshot dirs."""
    evidence = []
    if os.path.isdir(screenshot_dir):
        for fname in sorted(os.listdir(screenshot_dir)):
            fpath = os.path.join(screenshot_dir, fname)
            if os.path.isfile(fpath):
                evidence.append(fpath)
    for name in ["stdout.log", "stderr.log", "prompt.txt"]:
        fpath = os.path.join(artifact_dir, name)
        if os.path.isfile(fpath):
            evidence.append(fpath)
    if extra_files:
        for name in extra_files:
            fpath = os.path.join(artifact_dir, name)
            if os.path.isfile(fpath):
                evidence.append(fpath)
    return evidence


def write_output(path, data):
    """Write CLIOutput JSON."""
    with open(path, "w") as f:
        json.dump(data, f, indent=2)


def build_ai_command(prompt, max_steps, screenshot_dir, start_app=None, provider="gemini", model_name=None):
    """Build the ai-computer-use command list."""
    cmd = [
        "ai-computer-use",
        prompt,
        "--max-steps", str(max_steps),
        "--screenshot-dir", screenshot_dir,
        "--no-write-env",
    ]
    if start_app and start_app not in ("the app", ""):
        cmd.extend(["--start-app", start_app])
    cmd.extend(["--provider", provider])
    if model_name:
        cmd.extend(["--model", model_name])
    return cmd


def parse_outcome(result, elapsed):
    """Parse outcome from subprocess result using structured verdict + fallback.

    Args:
        result: subprocess.CompletedProcess with .stdout, .stderr, .returncode
        elapsed: float seconds elapsed

    Returns:
        (outcome, summary) tuple where outcome is "pass" or "fail".
    """
    # Try structured verdict first
    structured, data = parse_structured_verdict(result.stdout)
    if structured:
        outcome = structured
        summary = f"Agent verdict: {structured.upper()} ({elapsed:.1f}s)"
        return outcome, summary

    if result.returncode == 0:
        # Fallback: keyword match only on last 500 chars (conclusion, not narration)
        tail = result.stdout[-500:].lower() if result.stdout else ""
        if any(w in tail for w in ["fail", "incorrect", "wrong"]):
            outcome = "fail"
            summary = f"Agent completed but reported issues ({elapsed:.1f}s): {result.stdout[-300:]}"
        else:
            outcome = "pass"
            summary = f"Agent completed successfully ({elapsed:.1f}s): {result.stdout[-300:]}"
    else:
        outcome = "fail"
        summary = f"Agent failed (exit {result.returncode}, {elapsed:.1f}s): {result.stderr[:300]}"

    return outcome, summary
