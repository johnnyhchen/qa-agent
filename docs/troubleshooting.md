# Troubleshooting

## Docker Setup Fails

- Verify Docker Desktop is running.
- Confirm `DOCKER_BIN` points to a callable binary.
- Retry with `qa-agent trace --run-id <run_id>` and inspect docker logs under run artifacts.

## Browser Automation Connectivity

- Ensure `AI_BROWSER_USE_BIN` is installed and callable.
- Verify browser extension/websocket settings expected by your local `ai-browser-use` install.
- Run the doctor path in web adapter tests or call the binary with `--version`.

## macOS Automation Permissions

- Ensure Accessibility and Screen Recording permissions are granted for terminal/runner processes.
- Confirm `AI_COMPUTER_USE_BIN` resolves in `PATH`.
- Treat macOS outcomes as experimental and review stability hints.

## iOS Runs Return Blocked

- The iOS runner is intentionally a stub until simulator driver integration is configured.
- Install Xcode command line tools and verify `xcrun` is available.

