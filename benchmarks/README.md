# QA-Agent Benchmarks

Non-trivial applications with **seeded flaws** for regression-testing the qa-agent pipeline across all four surfaces.

## Quick Start

```bash
# All benchmarks (API always runs; web needs browser serve; macOS/iOS need API key + tools)
go test -v -timeout 600s ./benchmarks/ -run TestBenchmark

# Individual surfaces
go test -v ./benchmarks/ -run TestBenchmark_API
go test -v ./benchmarks/ -run TestBenchmark_Web
go test -v ./benchmarks/ -run TestBenchmark_MacOS
go test -v ./benchmarks/ -run TestBenchmark_iOS
go test -v ./benchmarks/ -run TestBenchmark_FullReport
```

## Prerequisites

| Surface | Dependencies | Setup |
|---------|-------------|-------|
| API | None | Always works |
| Web | `browser-cli`, running browser server | `browser serve --backend selenium --headless` |
| macOS | `swift`, `ai-computer-use`, API key | `export GEMINI_API_KEY=...` (or `ANTHROPIC_API_KEY`) |
| iOS | `xcodebuild`, `xcrun simctl`, `ai-computer-use`, API key | Xcode + simulator runtime installed |

### Provider Setup (macOS / iOS)

The macOS and iOS benchmarks use `ai-computer-use` with a vision-capable LLM. By default, they use **Gemini** (`gemini-3-flash-preview`) for its initial screenshot capture and low temperature (0.2).

```bash
# Gemini (default, recommended)
export GEMINI_API_KEY=your-gemini-api-key

# Or Anthropic (fallback)
export ANTHROPIC_API_KEY=your-anthropic-api-key
QA_BENCH_PROVIDER=anthropic go test -v ./benchmarks/ -run TestBenchmark_MacOS
```

| Env Var | Default | Description |
|---------|---------|-------------|
| `GEMINI_API_KEY` | — | Gemini API key (default provider) |
| `ANTHROPIC_API_KEY` | — | Anthropic API key (fallback) |
| `QA_BENCH_PROVIDER` | `gemini` | Provider: `gemini` or `anthropic` |
| `QA_BENCH_MODEL` | `gemini-3-flash-preview` | Model name passed to `ai-computer-use` |

### Web Setup

```bash
pip install browser-cli
pip install selenium
browser serve --backend selenium --headless  # in a separate terminal
```

## Applications

### API Server (`api/server.go`)

REST API with 7 seeded bugs. Runs as `httptest.Server` in-process — no external dependencies.

| Bug | Endpoint | Correct | Flaw |
|-----|----------|---------|------|
| API-1 | POST /login | 401 for bad creds | 200 for any credentials |
| API-2 | GET /users | 401 without auth | 200 without auth check |
| API-3 | GET /users/1 | Content-Type: application/json | text/plain |
| API-4 | GET /users/999 | 404 | 200 with empty user |
| API-5 | POST /users/create | 201 | 200 |
| API-6 | DELETE /users/1 | 204 | 200 with body |
| API-7 | GET /search | Valid JSON | Broken JSON |

### Web Task Manager (`web/app/`)

Multi-page task management app with login, dashboard stats, CRUD, search, and filtering. The `ai-browser-use` adapter drives the `browser` CLI to automate a real browser.

| Bug | Page | Correct | Flaw |
|-----|------|---------|------|
| WEB-1 | Login | Error message on bad creds | Error never appears |
| WEB-2 | Dashboard | Stats update on task changes | Stats always show 0 |
| WEB-3 | Task list | Completed tasks show strikethrough | CSS class never applied |
| WEB-4 | Task list | Delete button removes task | No click handler |
| WEB-5 | Login | Valid login shows dashboard | Redirect to 404 |

### Electron Notes App (`electron/`)

Note-taking desktop app with sidebar, search, dark mode, and persistence. Tested via `ai-computer-use` or browser automation.

| Bug | Feature | Correct | Flaw |
|-----|---------|---------|------|
| ELEC-1 | Dark mode | Toggle applies dark class | Toggle does nothing |
| ELEC-2 | Note count | Shows filtered count | Shows total (ignores filter) |
| ELEC-3 | Search | Searches title + body | Only searches titles |
| ELEC-4 | Save | Persists notes to disk | Writes empty array |
| ELEC-5 | Delete | Removes from data + UI | Removes from UI only |

### macOS Unit Converter (`macos/UnitConverter.swiftpm/`)

SwiftUI macOS app with temperature/length/weight/volume conversions and history panel. The benchmark builds it into a `.app` bundle so `ai-computer-use --start-app` can find it via LaunchServices.

```bash
# Build .app bundles (used automatically by the test)
bash benchmarks/macos/build.sh clean
bash benchmarks/macos/build.sh buggy
# Verify manually
open benchmarks/macos/UnitConverter-clean.app
```

| Bug | Feature | Correct | Flaw |
|-----|---------|---------|------|
| MAC-BUG-1 | Temperature | 100°C = 212°F | +23 instead of +32 |
| MAC-BUG-2 | Length | 1 mi = 1.6093 km | Uses 1000 instead of 1609.34 |
| MAC-BUG-3 | Clear history | Removes all entries | Only removes first entry |
| MAC-BUG-4 | Toggle history | Hides/shows panel | Button does nothing |
| MAC-BUG-5 | History count | 1 entry per conversion | 2 entries per conversion |

### iOS Contacts App (`ios/ContactsApp.swiftpm/`)

SwiftUI iOS contacts manager with add/edit/delete, search, and favorites. The benchmark builds it for the iOS Simulator with `xcodebuild`, then installs and launches it in the simulator. `ai-computer-use` interacts with the Simulator window (it's just a macOS app).

```bash
# Build for simulator (used automatically by the test)
bash benchmarks/ios/build.sh clean
bash benchmarks/ios/build.sh buggy
# Manual verification
xcrun simctl boot "iPhone 17"
xcrun simctl install booted /tmp/contactsapp-build-clean/.../ContactsApp.app
xcrun simctl launch booted ContactsApp
```

| Bug | Feature | Correct | Flaw |
|-----|---------|---------|------|
| IOS-BUG-1 | Name display | "First Last" | "Last First" |
| IOS-BUG-2 | Add contact | Sorted insertion | Appended unsorted |
| IOS-BUG-3 | Delete | Removes selected | Off-by-one (wrong contact) |
| IOS-BUG-4 | Search | Matches first+last+email | Only matches first name |
| IOS-BUG-5 | Favorites count | Shows actual count | Always shows 0 |

## Architecture

```
benchmarks/
├── api/
│   └── server.go                         # REST API (-mode clean|buggy)
├── web/
│   ├── app/{index,buggy}.html + app.js   # Task manager
│   └── serve.go                          # Static file server
├── electron/
│   ├── package.json + main.js            # Electron app
│   ├── preload.js                        # IPC bridge
│   └── index.html                        # Notes UI
├── macos/
│   ├── scenarios.json                    # Test scenarios
│   └── UnitConverter.swiftpm/            # SwiftUI macOS app
├── ios/
│   ├── scenarios.json                    # Test scenarios (stub)
│   └── ContactsApp.swiftpm/             # SwiftUI iOS app
├── adapters/
│   ├── adapter_utils.py                  # Shared: prompt builder, verdict parser, evidence
│   ├── ai-browser-use                    # Python: JSON → browser CLI → JSON
│   ├── ai-computer-use-adapter           # Python: JSON → ai-computer-use → JSON
│   └── ai-ios-simulator-adapter          # Python: JSON → simctl + ai-computer-use → JSON
└── run_benchmarks_test.go                # Integration tests for all surfaces
```

## How It Works

1. Start clean/buggy dummy applications (in-process or external)
2. Construct `model.Task` objects targeting those apps
3. Run through the full orchestrator pipeline: planner → queue → executor → judge
4. Compare verdicts against known expected outcomes
5. Report precision/recall metrics

The **web**, **macOS**, and **iOS** surfaces call real external tools (`browser` CLI, `ai-computer-use`, `xcrun simctl`) via adapter scripts that translate the qa-agent JSON protocol.
