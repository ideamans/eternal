# Eternal

ターミナルセッション永続化マネージャ。任意のコマンドをサーバー側のPTYで実行し、SSH切断後もプロセスを維持。CLIまたはWebブラウザから再接続できます。

```
et run htop ──WS──▶ et server ──pty──▶ htop
                          │
                     WebSocket
                          │
                          ▼
                   Browser (xterm.js)
```

## 特徴

- **セッション永続化** — PTYはサーバー側で保持。SSH切断してもプロセスは生き続ける。
- **Web UI** — ブラウザからxterm.js経由で任意のセッションに接続可能。表示サイズはCLIターミナルに追従。
- **複数クライアント同時接続** — 同じセッションにCLI・ブラウザから複数接続可能。
- **シングルバイナリ** — `et` 1つでサーバーもクライアントもWeb UIもすべて提供。
- **簡単デーモンインストール** — `et install | bash` でlaunchd (macOS) / systemd (Linux) に登録。
- **自動クリーンアップ** — コマンド終了 (Ctrl-C、`:q`、`exit` 等) でセッション自動削除。

## クイックスタート

### 1. サーバー起動

```bash
et server
```

デーモンとしてインストール:

```bash
et install | bash
```

### 2. コマンド実行

```bash
et run htop
```

引数なしで現在のシェル (`$SHELL`) を起動:

```bash
et run
```

### 3. 切断して再接続

SSHを切断してもプロセスは継続。後から再接続:

```bash
et attach <名前|ID>
```

### 4. Web UIから操作

ブラウザで `http://<ホスト>:2840` を開く。セッション一覧からクリックで接続。

## コマンド一覧

| コマンド | 説明 |
|---|---|
| `et server` | サーバーデーモン起動 (デフォルト: 127.0.0.1:2840) |
| `et run [コマンド] [引数...]` | 新規永続セッションでコマンド実行 |
| `et run --name work vim todo.md` | 名前付きセッション |
| `et attach <名前\|ID>` | 既存セッションに再接続 |
| `et ls` | セッション一覧 |
| `et kill <名前\|ID>` | セッション強制終了 (SIGKILL) |
| `et install` | プラットフォーム用デーモンインストールスクリプトを出力 |

## セッションライフサイクル

```
et run htop
    │
    ▼
 Active (クライアント接続中、プロセス実行中)
    │
    ├── SSH切断 ──▶ Detached (クライアント0、プロセスは生存)
    │                  ├── et attach ──▶ Active (復帰)
    │                  ├── ブラウザ接続 ──▶ Active
    │                  └── et kill ──▶ Dead
    │
    ├── Ctrl-C / exit (プロセス自然終了) ──▶ Dead (自動削除)
    │
    └── ブラウザからKill ──▶ Dead
```

## Web UI

組み込みWeb UIの機能:

- サイドバーにセッション一覧 (作業ディレクトリでグループ化)
- xterm.jsターミナル (CLIターミナルのサイズに追従、ブラウザが小さい場合は縮小表示)
- セッションごとのKillボタン
- サーバーホスト名表示

## ビルド

必要環境: Go 1.21+, Node.js 18+

```bash
make build
```

`web/` でnpm build → `cmd/et/dist/` にコピー → Goバイナリビルドを実行します。

## プロジェクト構成

```
eternal/
├── cmd/et/          # シングルバイナリ (サーバー + クライアント)
├── pkg/
│   ├── session/     # PTY管理、ライフサイクル、スクロールバックバッファ
│   ├── server/      # HTTP/WSサーバー、REST API、Web UI配信
│   ├── client/      # WebSocketクライアントライブラリ
│   └── protocol/    # WebSocketメッセージ型定義
├── web/             # TypeScript + Vite + Tailwind CSS 4 サブプロジェクト
│   └── src/
└── Makefile
```

## 設定

デフォルトポート: **2840** (127.0.0.1のみ)

```bash
et server --host 0.0.0.0 --port 3000
```

## 制限事項

- サーバー再起動時にセッションは失われます。PTYプロセスはサーバーの子プロセスであるため、サーバー停止で全プロセスが終了します。
- 現バージョンでは認証機能がありません。localhostにバインドするか、SSHトンネル経由でのアクセスを推奨します。

## ライセンス

MIT
