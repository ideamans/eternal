# Eternal - Persistent Terminal Session Manager

## コンセプト

「ターミナルセッションをサーバー側で永続化し、どこからでも再接続できる」

tmuxの再発明ではなく、**PTYセッションをネットワーク越しに管理するマネージャ**。
Teleportの軽量版とも言える。

## 名前

- プロダクト名: **eternal** - セッションが永続する本質を表現
- CLIコマンド: **`et`** - 短く、タイプしやすい
- サーバー起動: **`et server`** - シングルバイナリ、サブコマンドで役割切替

---

## アーキテクチャ

```
[SSH login]
   │
   ▼
et run htop ──WS──▶ et server ──pty──▶ htop (任意のコマンド)
                          │
                     WebSocket
                          │
                          ▼
                   Browser (xterm.js)
```

**核心: PTYはサーバー側で保持する。** SSH切断してもプロセスは生き続ける。
コマンドのプロセスが終了すると、セッションも自動的に消滅する。

### コンポーネント

シングルバイナリ (`et`) ですべての機能を提供。サブコマンドで役割を切り替える。

| サブコマンド | 役割 | 技術 |
|---|---|---|
| `et server` | デーモン起動 (PTY管理・WS中継・REST API・Web UI) | Go, gorilla/websocket, creack/pty |
| `et run` / `et attach` / `et ls` / `et kill` | CLIクライアント | Go, WebSocket client |
| (Web UI) | ブラウザからセッション操作 (serverに同梱) | xterm.js |

---

## セッションモデル

```go
type Session struct {
    ID        string
    Name      string              // ユーザー指定の名前 (例: "default", "debug")
    Command   []string            // 実行コマンド (例: ["htop"], ["vim", "file.txt"])
    Cmd       *exec.Cmd
    Pty       *os.File
    Clients   map[string]*Client  // 接続中のクライアント (複数可)
    Rows      int
    Cols      int
    ExitCode  *int                // プロセス終了コード (nil = 実行中)
    CreatedAt time.Time
    LastUsed  time.Time
}

type Client struct {
    ID   string
    Conn *websocket.Conn
    Type string // "terminal" or "browser"
}
```

### プロセス終了監視

サーバーは各セッションのプロセスを goroutine で `cmd.Wait()` し、
終了を検知したら全クライアントに通知してセッションを破棄する。

```go
go func() {
    exitCode := 0
    if err := s.Cmd.Wait(); err != nil {
        var exitErr *exec.ExitError
        if errors.As(err, &exitErr) {
            exitCode = exitErr.ExitCode()
        }
    }
    s.ExitCode = &exitCode
    s.notifyAllClients(Message{Type: "exit", ExitCode: exitCode})
    s.closeAllClients()
    sessionManager.Remove(s.ID)
}()
```

**セッション終了のトリガー:**

| トリガー | 動作 |
|---|---|
| コマンドが自然終了 (exit, Ctrl-C等) | プロセス終了 → セッション自動消滅 |
| WebブラウザからKill | プロセスにSIGTERM送信 → プロセス終了 → セッション消滅 |
| `et kill <ID>` | 同上 |
| SSH切断 | **何も起きない** (セッション維持、後で再接続可能) |

---

## CLI設計

```bash
# セッション管理
et run <COMMAND> [ARGS...]       # コマンドを永続セッションで実行 & attach
et run --name work vim todo.md   # 名前付きセッション
et run htop                      # 例: htopを永続実行
et attach [NAME|ID]              # 既存セッションに再接続
et ls                            # セッション一覧
et kill <NAME|ID>                # セッション強制終了 (SIGTERM)

# サーバー
et server                   # デーモン起動 (フォアグラウンド)
et server --port 2840       # ポート指定で起動

# デーモンインストール
et install                  # プラットフォーム判別し、インストール用シェルスクリプトを stdout に出力
et install | bash           # パイプで即実行
```

### 典型的なワークフロー

```bash
# 1. SSHでサーバーに接続し、TUIツールを永続実行
ssh myserver
et run htop

# 2. SSHが切断されても htop は生き続ける

# 3a. ブラウザから確認・操作
#     http://myserver:2840 → セッション一覧 → htop に接続

# 3b. または SSH で再接続して復帰
ssh myserver
et attach htop-xxxx   # セッションIDまたは名前で再接続

# 4. 終了方法 (どれでもOK)
#    - TUI内で通常終了 (Ctrl-C, :q 等) → セッション自動消滅
#    - ブラウザからKillボタン → プロセス終了 → セッション消滅
#    - et kill <ID>
```

---

## `et install` - デーモンインストーラ

### コンセプト

`et install` はシェルスクリプトを **stdout に出力するだけ**。実行はしない。
ユーザーが内容を確認してから `et install | bash` でパイプ実行する設計。

```bash
# スクリプトを確認
et install

# そのまま実行
et install | bash
```

### プラットフォーム判別

`et` バイナリ自身が `runtime.GOOS` と追加情報から判別し、適切なスクリプトを生成する。

| プラットフォーム | 判別方法 | init system |
|---|---|---|
| macOS | `runtime.GOOS == "darwin"` | launchd (plist) |
| Linux (systemd) | `runtime.GOOS == "linux"` + systemd検出 | systemd (unit file) |

systemd検出: `/run/systemd/system` の存在確認。

### 生成するスクリプトの処理内容

#### 共通

1. `et` バイナリの所在確認 (`which et` or 実行中バイナリのパス)
2. サービス定義ファイルの生成・配置
3. デーモンの有効化・起動
4. 状態確認メッセージの出力

#### macOS (launchd)

```bash
#!/bin/bash
set -eu

# -----------------------------------------------------------
# Eternal Terminal - macOS LaunchAgent Installer
# -----------------------------------------------------------
#
# This script installs "et server" as a launchd user agent.
# The server starts automatically on login and restarts on crash.
#
# Management commands:
#   Status:    launchctl list | grep com.eternal.et
#   Logs:      tail -f /tmp/eternal.log
#   Restart:   launchctl kickstart -k gui/$(id -u)/com.eternal.et
#   Stop:      launchctl bootout gui/$(id -u)/com.eternal.et
#   Disable:   launchctl bootout gui/$(id -u)/com.eternal.et && rm ~/Library/LaunchAgents/com.eternal.et.plist
#   Uninstall: launchctl bootout gui/$(id -u)/com.eternal.et 2>/dev/null; rm -f ~/Library/LaunchAgents/com.eternal.et.plist
# -----------------------------------------------------------

ET_BIN="$(which et)"
PLIST="$HOME/Library/LaunchAgents/com.eternal.et.plist"
DOMAIN="gui/$(id -u)"

mkdir -p "$HOME/Library/LaunchAgents"

cat > "$PLIST" << PLIST_EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.eternal.et</string>
    <key>ProgramArguments</key>
    <array>
        <string>${ET_BIN}</string>
        <string>server</string>
    </array>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/eternal.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/eternal.err</string>
</dict>
</plist>
PLIST_EOF

# Stop existing service if running, then bootstrap
launchctl bootout "$DOMAIN/com.eternal.et" 2>/dev/null || true
launchctl bootstrap "$DOMAIN" "$PLIST"

echo "eternal server installed and started."
echo "  Auto-start: enabled (RunAtLoad)"
echo "  Status:     launchctl list | grep com.eternal.et"
echo "  Logs:       tail -f /tmp/eternal.log"
echo "  Restart:    launchctl kickstart -k $DOMAIN/com.eternal.et"
echo "  Uninstall:  launchctl bootout $DOMAIN/com.eternal.et && rm $PLIST"
```

#### Linux (systemd)

ユーザーサービス (`--user`) として登録。root不要。
`loginctl enable-linger` でログアウト後もサービスを維持する。

```bash
#!/bin/bash
set -eu

# -----------------------------------------------------------
# Eternal Terminal - systemd User Service Installer
# -----------------------------------------------------------
#
# This script installs "et server" as a systemd user service.
# The server starts automatically on boot (via linger) and
# restarts on crash.
#
# Management commands:
#   Status:    systemctl --user status eternal.service
#   Logs:      journalctl --user -u eternal.service -f
#   Restart:   systemctl --user restart eternal.service
#   Stop:      systemctl --user stop eternal.service
#   Disable:   systemctl --user disable eternal.service
#   Uninstall: systemctl --user stop eternal.service && systemctl --user disable eternal.service && rm ~/.config/systemd/user/eternal.service && systemctl --user daemon-reload
# -----------------------------------------------------------

ET_BIN="$(which et)"
UNIT_DIR="$HOME/.config/systemd/user"
mkdir -p "$UNIT_DIR"

cat > "$UNIT_DIR/eternal.service" << UNIT_EOF
[Unit]
Description=Eternal Terminal Session Manager
After=network.target

[Service]
Type=simple
ExecStart=${ET_BIN} server
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
UNIT_EOF

systemctl --user daemon-reload
systemctl --user enable eternal.service
systemctl --user start eternal.service

# Enable linger so the service survives logout
loginctl enable-linger "$(whoami)" 2>/dev/null || true

echo "eternal server installed and started."
echo "  Auto-start: enabled (systemd enable + linger)"
echo "  Status:     systemctl --user status eternal.service"
echo "  Logs:       journalctl --user -u eternal.service -f"
echo "  Restart:    systemctl --user restart eternal.service"
echo "  Uninstall:  systemctl --user stop eternal.service && systemctl --user disable eternal.service && rm $UNIT_DIR/eternal.service"
```

### `et install` の実装方針

```go
func installCmd() *cobra.Command {
    return &cobra.Command{
        Use:   "install",
        Short: "Output daemon install script for this platform",
        Run: func(cmd *cobra.Command, args []string) {
            script := generateInstallScript() // runtime.GOOS で分岐
            fmt.Print(script)                 // stdout に出力するだけ
        },
    }
}
```

### 対応プラットフォーム (Phase 1)

| OS | init system | 優先度 |
|---|---|---|
| macOS | launchd (LaunchAgents) | MVP |
| Linux (Ubuntu, Debian, Fedora, etc.) | systemd (user service) | MVP |

未対応プラットフォームではエラーメッセージと手動セットアップ手順を出力する。

---

## サーバー API設計

### WebSocket エンドポイント

| パス | 用途 |
|---|---|
| `GET /ws/session/{id}` | セッションのターミナルI/O (双方向) |

WebSocketメッセージ形式:

```
// クライアント → サーバー
{ "type": "input",  "data": "base64..." }
{ "type": "resize", "cols": 120, "rows": 40 }

// サーバー → クライアント
{ "type": "output", "data": "base64..." }
{ "type": "exit",   "exit_code": 0 }
```

### REST API

| メソッド | パス | 用途 |
|---|---|---|
| `GET`    | `/api/sessions`      | セッション一覧 |
| `POST`   | `/api/sessions`      | 新規セッション作成 |
| `GET`    | `/api/sessions/{id}` | セッション詳細 |
| `DELETE` | `/api/sessions/{id}` | セッション終了 |

### セッション作成リクエスト

```json
{
  "name": "work",
  "command": ["vim", "todo.md"],
  "cols": 120,
  "rows": 40
}
```

### セッション一覧レスポンス

```json
[
  {
    "id": "abc123",
    "name": "default",
    "command": ["htop"],
    "clients": 2,
    "created_at": "2026-03-18T10:00:00Z",
    "last_used": "2026-03-18T15:30:00Z"
  }
]
```

---

## サーバー内部設計

### I/Oフロー

```
PTY stdout ──read──▶ Broadcast to all Clients
                         │
              ┌──────────┼──────────┐
              ▼          ▼          ▼
          Client A   Client B   Browser

Client input ──▶ Write to PTY stdin
```

複数クライアントが同時接続可能。全員が同じ画面を見る（ペアプロ向き）。

### セッションライフサイクル

```
et run htop
    │
    ▼
 Active (CLIクライアント接続中、htop実行中)
    │
    ├── SSH切断 ──▶ Detached (クライアント0、プロセスは生存)
    │                  │
    │                  ├── et attach ──▶ Active (復帰)
    │                  ├── ブラウザ接続 ──▶ Active (ブラウザ経由操作)
    │                  └── et kill ──▶ Dead (SIGTERM → プロセス終了)
    │
    ├── Ctrl-C / exit (プロセスが自然終了) ──▶ Dead (自動消滅)
    │
    └── ブラウザからKill ──▶ Dead (SIGTERM → プロセス終了)

Dead: セッションはサーバーから削除、全クライアントに通知
```

---

## Web UI設計

### ページ構成

| パス | 内容 |
|---|---|
| `/` | セッション一覧 (ダッシュボード) |
| `/session/{id}` | ターミナル画面 (xterm.js) |

### ダッシュボード機能

- セッション一覧表示 (名前, 状態, 接続数, 最終使用)
- 新規セッション作成ボタン
- セッションkillボタン
- セッションクリックでattach

### ターミナル画面

- xterm.js でフル機能ターミナル
- WebSocket接続
- リサイズ自動追従

### 技術スタック

| 技術 | 用途 |
|---|---|
| TypeScript | 型安全なフロントエンド開発 |
| Vite | ビルドツール (dev server + production build) |
| Tailwind CSS 4 | ユーティリティファーストCSS |
| xterm.js | ターミナルエミュレータ |
| @xterm/addon-fit | ターミナル自動リサイズ |
| @xterm/addon-webgl | GPU描画 (パフォーマンス向上) |

### サブプロジェクト構成 (`web/`)

`web/` ディレクトリはTypeScript + Viteの独立したサブプロジェクト。
Viteビルドの出力先を `web/dist/` に配置し、Go側で `embed` する。

```
web/
├── package.json
├── tsconfig.json
├── vite.config.ts
├── src/
│   ├── main.ts           # エントリポイント (ルーティング)
│   ├── main.css           # Tailwind CSS 4 (@import "tailwindcss")
│   ├── pages/
│   │   ├── dashboard.ts   # セッション一覧
│   │   └── terminal.ts    # ターミナル画面 (xterm.js)
│   ├── lib/
│   │   ├── api.ts         # REST API クライアント
│   │   └── ws.ts          # WebSocket ラッパー
│   └── index.html         # SPA エントリHTML
├── dist/                  # ← vite build 出力先 (gitignore, Go embed対象)
│   ├── index.html
│   └── assets/
│       ├── main-[hash].js
│       └── main-[hash].css
```

### package.json (主要部分)

```json
{
  "name": "eternal-web",
  "private": true,
  "type": "module",
  "scripts": {
    "dev": "vite",
    "build": "tsc && vite build",
    "preview": "vite preview"
  },
  "dependencies": {
    "@xterm/xterm": "^5",
    "@xterm/addon-fit": "^0.10",
    "@xterm/addon-webgl": "^0.18"
  },
  "devDependencies": {
    "typescript": "^5.7",
    "vite": "^6",
    "tailwindcss": "^4",
    "@tailwindcss/vite": "^4"
  }
}
```

### vite.config.ts

```ts
import { defineConfig } from 'vite'
import tailwindcss from '@tailwindcss/vite'

export default defineConfig({
  root: 'src',
  plugins: [tailwindcss()],
  build: {
    outDir: '../dist',
    emptyOutDir: true,
  },
})
```

### main.css

```css
@import 'tailwindcss';
```

Tailwind CSS 4 はCSSファイル内で設定を完結させる。`tailwind.config.js` は不要。

### 開発ワークフロー

```bash
# Web UI 開発 (HMR)
cd web && npm run dev

# プロダクションビルド → web/dist/ に出力
cd web && npm run build

# Go バイナリビルド (web/dist/ を embed)
go build ./cmd/et
```

### Go側の embed 統合

```go
package server

import "embed"

//go:embed all:../web/dist
var webAssets embed.FS
```

`web/dist/` がない場合 (開発中) はGoビルドが失敗するため、
`.gitkeep` または空の `index.html` を `web/dist/` に配置しておく。

### 開発時のプロキシ構成

Vite dev server 使用時は、API/WSリクエストを `et server` にプロキシする。

```ts
// vite.config.ts (dev時追加)
export default defineConfig({
  // ...
  server: {
    proxy: {
      '/api': 'http://localhost:2840',
      '/ws': {
        target: 'ws://localhost:2840',
        ws: true,
      },
    },
  },
})
```

---

## プロジェクト構成

```
eternal/
├── cmd/
│   └── et/             # シングルバイナリ (server/client両方)
│       └── main.go
├── pkg/
│   ├── session/        # セッション管理 (PTY, ライフサイクル)
│   ├── server/         # HTTP/WSサーバー (web/dist を embed)
│   ├── client/         # WSクライアントライブラリ
│   └── protocol/       # WS メッセージ型定義
├── web/                # TypeScript + Vite サブプロジェクト
│   ├── package.json
│   ├── tsconfig.json
│   ├── vite.config.ts
│   ├── src/
│   │   ├── index.html
│   │   ├── main.ts
│   │   ├── main.css
│   │   ├── pages/
│   │   │   ├── dashboard.ts
│   │   │   └── terminal.ts
│   │   └── lib/
│   │       ├── api.ts
│   │       └── ws.ts
│   └── dist/           # vite build 出力先 (Go embed 対象)
├── Makefile            # ビルド統合
├── go.mod
├── go.sum
└── plan.md
```

---

## 依存ライブラリ

### Go

| ライブラリ | 用途 |
|---|---|
| `github.com/creack/pty` | PTY生成・制御 |
| `github.com/gorilla/websocket` | WebSocket (サーバー&クライアント) |
| `github.com/spf13/cobra` | CLI フレームワーク |
| `golang.org/x/term` | ターミナルrawモード制御 |

### Web (npm)

| パッケージ | 用途 |
|---|---|
| `@xterm/xterm` | ターミナルエミュレータ |
| `@xterm/addon-fit` | 自動リサイズ |
| `@xterm/addon-webgl` | GPU描画 |
| `vite` | ビルドツール |
| `typescript` | 型チェック |
| `tailwindcss` | CSS |
| `@tailwindcss/vite` | Vite統合プラグイン |

---

## ビルド

### Makefile

```makefile
.PHONY: all build web go clean dev

all: build

# Web UI ビルド → Go embed → バイナリ
build: web go

web:
	cd web && npm install && npm run build

go:
	go build -o et ./cmd/et

clean:
	rm -rf web/dist et

# 開発用: Web HMR + Go サーバー並行起動
dev:
	cd web && npm run dev &
	go run ./cmd/et server
```

---

## 設定

`~/.config/eternal/config.yaml` or 環境変数:

```yaml
server:
  host: "127.0.0.1"
  port: 2840
  # token: "optional-auth-token"

default_command: "/bin/zsh"    # et run (引数なし) 時のデフォルト
```

CLIはデフォルトで `localhost:2840` に接続。

---

## セキュリティ考慮

### Phase 1 (ローカル用途)

- `127.0.0.1` のみバインド (デフォルト)
- Unixソケット対応も検討

### Phase 2 (リモート用途、将来)

- トークンベース認証
- TLS対応
- セッションごとのアクセス制御

---

## 実装フェーズ

### Phase 1: 最小動作 (MVP)

**ゴール: `et run` でセッション作成、`et attach` で再接続できる**

1. `pkg/session` - PTY生成・管理
2. `pkg/protocol` - WSメッセージ型
3. `pkg/server` - WS/REST サーバー
4. `pkg/client` - WSクライアント
5. `cmd/et` - シングルバイナリ (cobra: `server`, `run`, `attach`, `ls`, `kill`)

### Phase 2: Web UI

6. `web/` サブプロジェクト初期化 (Vite + TypeScript + Tailwind CSS 4)
7. xterm.js ターミナル画面
8. セッション一覧ダッシュボード (REST API連携)
9. Vite build → `web/dist/` → Go `embed` でサーバーバイナリに同梱
10. Makefile でビルド統合

### Phase 3: UX強化

10. `attach-or-create` コマンド
11. セッション名前付き管理
12. 設定ファイル対応
13. ターミナルリサイズ同期

### Phase 4: 将来機能

- 認証・TLS
- セッションログ保存・replay
- タグ付け
- 複数ユーザー対応
- systemd / launchd サービス設定

---

## tmuxとの比較

| 機能 | tmux | eternal |
|---|---|---|
| セッション永続化 | Yes | Yes |
| Web UI | No | Yes |
| REST API | No | Yes |
| 複数クライアント同時接続 | 限定的 | Yes (設計の中心) |
| ブラウザからアクセス | No | Yes |
| tmux不要 | - | Yes |
| リモートアクセス (将来) | SSH依存 | WebSocket |

---

## 技術メモ

### PTYサーバー側保持が必須な理由

- クライアント側でPTY保持 → SSH切断でプロセス死亡
- サーバー側でPTY保持 → SSH切断に無関係、CLIは単なるビューア

### WebSocket選択の理由

- 双方向リアルタイム通信
- ブラウザとCLI両方で使える
- HTTP互換でプロキシ通過しやすい

### xterm.js

- 事実上のWeb標準ターミナルエミュレータ
- VS Code, Jupyterでも採用
- 完全なVT100+互換
