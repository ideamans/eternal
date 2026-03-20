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
- **セッション集約** — 複数のeternalサーバーに接続し、1つのWeb UIですべてのセッションを一覧表示。

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
| `et server` | サーバーデーモン起動 (デフォルト: 0.0.0.0:2840) |
| `et server --peer host1 --peer host2:3000` | ピアサーバー指定でセッション集約 |
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

## セッション集約

複数のeternalサーバーのセッションを1つのWeb UIで一覧表示・操作できます。ブラウザが各ピアサーバーのAPIとWebSocketに直接接続します。

### 使い方

`--peer` フラグまたは `ET_PEERS` 環境変数でピアサーバーを指定:

```bash
# CLIフラグで指定 (--peer ごとに1つ)
et server --peer server-a.local --peer server-b.local:3000

# 環境変数で指定 (カンマ区切り)
export ET_PEERS=server-a.local,server-b.local:3000
et server

# 両方を併用 (マージされる)
ET_PEERS=server-a.local et server --peer server-b.local
```

ポート省略時はデフォルトの **2840** が使用されます。スキーム省略時は `http://` が付与されます。

### 動作の仕組み

1. ローカルサーバーが `GET /api/peers` エンドポイントで設定済みのピアアドレス一覧を返す。
2. Web UIがピア一覧を取得し、各サーバーの `GET /api/info` と `GET /api/sessions` を並列にリクエスト。
3. サイドバーではピア設定時に **ホスト名:ディレクトリ名** でグループ化。ローカルのみの場合は従来通りディレクトリ名のみ。
4. WebSocket接続はブラウザから各サーバーに直接接続（ローカルサーバーを経由しない）。

### 前提条件

- ピアサーバーはブラウザから直接到達可能である必要があります（ローカルサーバーからだけでなく）。
- ピアサーバーの `/api/*` エンドポイントはデフォルトでCORSが有効です。

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

デフォルトポート: **2840** (0.0.0.0)

```bash
et server --host 0.0.0.0 --port 3000
```

| フラグ | 環境変数 | デフォルト | 説明 |
|---|---|---|---|
| `--host` | — | `0.0.0.0` | バインドするホスト |
| `--port` | — | `2840` | リッスンポート |
| `--peer` | `ET_PEERS` | (なし) | ピアサーバーアドレス (複数指定可)。`ET_PEERS` はカンマ区切り。 |

## 制限事項

- サーバー再起動時にセッションは失われます。PTYプロセスはサーバーの子プロセスであるため、サーバー停止で全プロセスが終了します。
- 現バージョンでは認証機能がありません。localhostにバインドするか、SSHトンネル経由でのアクセスを推奨します。

## ライセンス

MIT
