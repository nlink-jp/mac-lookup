# RFP: mac-lookup

> Generated: 2026-07-26
> Status: Draft

## 1. Problem Statement

パケットキャプチャ・無線サーベイ・DHCP/ARP ログ・EDR の資産情報などに現れる
MAC アドレス／BSSID から、製造元ベンダーと**アドレスの性質**を即座に判定する
CLI 兼 MCP サーバー。IEEE Registration Authority が公開するレジストリを
ローカルにキャッシュしてオフライン照合するため、調査対象に痕跡を残さない。

特に重要なのは、iOS/Android のランダム化 MAC や仮想 NIC（Locally Administered
Address）を「ベンダー不明」ではなく **「ベンダー照会が原理的に無意味なアドレス」**
として明示的に返すこと。ここを区別しないと、無線調査や IR において端末同定を
誤る。対象ユーザーは nlink-jp 運営者自身（IR・無線調査時）。

`asn-lookup`（IP → AS/国）、`tor-exit-lookup`（Tor Exit 判定）、
`icloud-relay-lookup`（Private Relay 判定）と同系統の「オフライン照合 lookup」
シリーズの一枚として、ネットワーク層に対する L2 側の視点を追加する。

## 2. Functional Specification

### Commands / API Surface

CLI サブコマンド構成は `tor-exit-lookup` にミラーする。

| Command | 説明 |
|---|---|
| `mac-lookup lookup <MAC>...` | MAC/BSSID/プレフィクスの順引き。複数引数・stdin バッチ対応 |
| `mac-lookup search <query>` | ベンダー名（部分一致）→ 割当プレフィクス一覧の逆引き |
| `mac-lookup update` | レジストリの再取得（条件付き GET） |
| `mac-lookup status` | キャッシュの鮮度・件数・レジストリ別内訳 |
| `mac-lookup mcp` | MCP サーバーとして stdio で起動 |

MCP tools（`asn-lookup` / `tor-exit-lookup` にミラー）:

| Tool | 説明 |
|---|---|
| `lookup_mac` | MAC/BSSID/プレフィクスの解決 |
| `search_vendor` | ベンダー名逆引き。件数が多いため **file-mediated**（`workspace_root` を受け取り `matches_file` を返す） |
| `db_status` | キャッシュ状態 |
| `update_db` | レジストリ再取得 |
| `get_usage` | ツールリファレンス + エラー復旧表（pinned `usage.md`） |

### 解決ロジック（本ツールの中核）

処理順序を固定する。**セマンティクス判定がベンダー照会より先**。

1. **正規化** — 受理する表記:
   - コロン区切り `00:11:22:33:44:55`
   - ハイフン区切り `00-11-22-33-44-55`
   - 区切りなし `001122334455`
   - Cisco ドット区切り `0011.2233.4455`
   - **部分プレフィクスのみ**（`00:11:22` / `8C:1F:64:AF:A`）— 24/28/36 bit の
     どの長さでも受理する
   - 大文字小文字を問わない
2. **broadcast 判定** — `FF:FF:FF:FF:FF:FF`
3. **I/G bit（先頭オクテットの bit 0）= 1 → multicast** — 既知の特殊アドレスは
   組み込みテーブルで名前を返す:

   | プレフィクス | 名称 |
   |---|---|
   | `01:80:C2` | IEEE 802.1D STP / LLDP / PAUSE 等 |
   | `01:00:5E` | IPv4 マルチキャスト（RFC 1112） |
   | `33:33` | IPv6 マルチキャスト（RFC 2464） |
   | `01:00:0C` | Cisco CDP / VTP / PVST+ |
   | `00:00:5E:00:01` | VRRP (IPv4, RFC 5798) |
   | `00:00:5E:00:02` | VRRP (IPv6, RFC 5798) |
   | `01:1B:19` / `01:80:C2:00:00:0E` | PTP (IEEE 1588) |

4. **U/L bit（先頭オクテットの bit 1）= 1 → Locally Administered** —
   ランダム化 MAC / 仮想 NIC / コンテナ等。**ベンダー照会は行わず、
   `vendor_lookup_applicable: false` を明示して返す**
5. **Universally Administered の場合のみ最長一致** — 36bit（MA-S / IAB）→
   28bit（MA-M）→ 24bit（MA-L）の順に照合。同一 24bit プレフィクスを複数の
   ベンダーが分割保有するため、先頭 6 桁のみの照合は誤答する
6. **`Private` 登録の区別** — IEEE は登録者名の非開示を認めている。
   組織名が `Private` の場合は「未割り当て」ではなく
   「登録済みだが名称非開示」として返す

### Input / Output

**text 出力**（既定）: 1 行 1 結果。ベンダー名を主とし、住所は出さない。

**`--json` 出力**: 1 入力につき 1 オブジェクト。スキーマ（ドラフト）:

```json
{
  "input": "8c:1f:64:af:a0:01",
  "mac": "8C:1F:64:AF:A0:01",
  "cast": "unicast",
  "administration": "universal",
  "vendor_lookup_applicable": true,
  "match": {
    "registry": "MA-S",
    "assignment": "8C1F64AFA",
    "prefix_bits": 36,
    "organization": "DATA ELECTRONIC DEVICES, INC",
    "address": "32 NORTHWESTERN DR SALEM NH US 03079",
    "private": false
  },
  "well_known": null
}
```

- `cast`: `unicast` | `multicast` | `broadcast`
- `administration`: `universal` | `local`
- `administration` が `local` のとき `match` は `null`、
  `vendor_lookup_applicable` は `false`、`note` にランダム化 MAC の可能性を記載
- `well_known`: multicast/特殊アドレスに該当する場合のみ `{name, prefix}`
- `address` は CSV の原文をそのまま保持する。**国コードの抽出は行わない**
  （フリーテキストで書式が不揃いのため、誤抽出のリスクが利得を上回る）

**終了コード（`tor-exit-lookup` 流の grep 風 tri-state）**:

| Code | 意味 |
|---|---|
| 0 | ベンダー確定 |
| 1 | 名前が出ない（未割り当て / LAA / multicast / `Private`） |
| 2 | エラー |

tri-state は **単一 MAC かつ text 出力時のみ**。複数入力・stdin・`--json` の
場合はバッチ扱いでエラー有無のみ（0 / 2）。

### Configuration

- 設定ファイル: sectioned TOML。パスは per-tool 個別に決める
  （scaffold 時に `tor-exit-lookup` の実装を確認して揃える）
- 設定項目: データディレクトリ / TTL / `auto_update` のみ
- **認証情報の項目は存在しない**
- 環境変数は `MAC_LOOKUP_` プレフィクスで設定ファイルを上書き

### External Dependencies

IEEE Registration Authority の公開 CSV 5 本のみ。認証不要。

| Registry | パス | 件数（2026-07-26 実測） | サイズ |
|---|---|---|---|
| MA-L (24bit) | `oui/oui.csv` | 39,812 | 3.8 MB |
| MA-M (28bit) | `oui28/mam.csv` | 6,501 | 740 KB |
| MA-S (36bit) | `oui36/oui36.csv` | 7,109 | 659 KB |
| IAB (36bit) | `iab/iab.csv` | 4,575 | 381 KB |
| CID | `cid/cid.csv` | 215 | 20 KB |

- ホスト: `standards-oui.ieee.org`
- 5 本とも **同一の 4 カラムスキーマ**
  （`Registry,Assignment,Organization Name,Organization Address`, UTF-8）→
  パーサは 1 本で足りる
- 合計 約 58,000 件 / 5.6 MB。索引を作らずメモリ上のマップで照合できる規模
- **データ本体はリポジトリに同梱しない**（実行時ダウンロード）

Go の外部依存はゼロ（`net/http` + `encoding/csv` + 標準ライブラリのみ）。

## 3. Design Decisions

**なぜ Go か** — 既存の lookup シリーズ（`asn-lookup` / `tor-exit-lookup` /
`icloud-relay-lookup`）と同じ言語・同じ骨格で、単一バイナリ配布と notarize の
作法をそのまま流用できる。外部依存ゼロで維持できるデータ規模でもある。

**骨格の移植元** — `tor-exit-lookup`（オフラインキャッシュ + `update`/`status` +
zero-dep JSON-RPC MCP + tri-state 終了コード）を主、`asn-lookup`（TTL と
`db_status` の作法）を従とする。

**補完関係にある既存ツール**:

- `asn-lookup` — L3 の帰属（IP → AS/国）に対し、本ツールは L2 の帰属（MAC → 製造元）
- `tor-exit-lookup` / `icloud-relay-lookup` / `abuse-lookup` — IP を多角プロファイル
  する群に対し、無線・LAN 側の観測点を追加する
- `ai-ir2` / `ir-hub` — IR 分析パイプラインから MCP 経由で参照する

**明示的なスコープ外**:

- Wireshark の `manuf` 等、二次集約リストの取り込み（GPL 混入回避。IEEE 原本のみ）
- リアルタイムの ARP / 無線スキャン、パケットキャプチャ
- BSSID 群解析（近接 BSSID から同一 AP/radio を推定する機能）は **v0.2 以降**。
  v0.1 は単一 MAC の正確な解決に集中する
- GUI
- IEEE の EtherType / Manufacturer ID / Operator ID の 3 ファイル
  （MAC アドレス解決に不要）

## 4. Development Plan

### Phase 1: Core

- レジストリパーサ（5 CSV 共通スキーマを 1 本で処理）
- 決定論的 store（保存順序を固定し差分を安定させる）
- 最長一致リゾルバ（36 → 28 → 24 bit）
- MAC 正規化 + セマンティクス判定（broadcast / multicast + 既知アドレス表 /
  LAA / universal）
- IEEE ダウンローダ（条件付き GET・TTL フロア・正直な User-Agent）
- config ローダ
- HTTP はインターフェース越しにしてモック可能にする

この時点で全ロジックが単体テスト可能であること。

### Phase 2: Features

- CLI: `lookup` / `search` / `update` / `status`、tri-state 終了コード、
  `--json`、stdin バッチ
- MCP サーバー（zero-dep JSON-RPC）: `lookup_mac` / `search_vendor` /
  `db_status` / `update_db` / `get_usage`
- `search_vendor` の file-mediated 出力（`workspace_root` → `matches_file`）

### Phase 3: Release

- README.md / README.ja.md / AGENTS.md / CHANGELOG.md / `usage.md`（pinned）
- 実データ E2E + ビルド済みバイナリでの実機シミュレーション
- `make build-all` → 4 platform、darwin arm64 は notarize + `spctl` 検証
- GitHub リリース（zip 内は canonical binary 名）
- homebrew-tap の formula（sha256 は公開 asset と一致検証）
- `cybersecurity-series` に submodule 追加
- org profile（アルファベット順）+ web catalog（EN/JA）の 2 面更新
- `check-org.sh` all green

3 フェーズはいずれも独立にレビュー可能。

## 5. Required API Scopes / Permissions

**None.** IEEE の公開 CSV は認証不要。API キー・OAuth スコープ・IAM ロールの
いずれも不要で、設定ファイルに認証項目を持たない。

## 6. Series Placement

Series: **cybersecurity-series**

Reason: MAC/BSSID 分析は IR および無線調査の文脈で使う。データ自体は中立な
資産情報であり `util-series` も候補だったが、`tor-exit-lookup` で採った
「照合方式より用途を優先して配置する」前例に従い、既存の `*-lookup` 群
（`whois` / `abuse` / `icloud-relay` / `tor-exit` / `doh` / `urlscan`）と
並べることで発見性を優先する。

## 7. External Platform Constraints

- **ブラウザ偽装 UA は 418 で拒否される** — `User-Agent: Mozilla/5.0` を送ると
  `418 I'm a Teapot` が返り、素の `curl/8.x` UA なら `200` が返る（2026-07-26 実測）。
  ブラウザ偽装をせず、ツール名を名乗る正直な User-Agent を使うこと
- **条件付き GET が使える** — 5 本とも `ETag` と `Last-Modified` を返すため、
  `If-None-Match` で `304` を取れる。無駄な 5.6 MB 転送を避ける
- **日次で再生成される** — 2026-07-26 時点で全ファイルの `Last-Modified` が
  同日 00:01 UTC 前後。更新頻度は 1 日 1 回程度と想定してよい
- **IEEE 側への礼儀** — `tor-exit-lookup` と同様に TTL にフロアを設け、
  短時間の連続取得を防ぐ
- **ライセンス** — IEEE のページに明示的な利用条件の記載はない。データ本体を
  リポジトリに同梱せず実行時ダウンロードとすることで、再配布の論点を回避する
- **`Private` 登録の存在** — 登録者が名称非開示を選べるため、
  「割り当て済みだが名前が引けない」ケースが構造的に存在する（MA-M で多い）

---

## Discussion Log

**2026-07-26 — 発案と方向づけ**

- 発端: 「lookup シリーズにもう一つ OUI コードの lookup があると、MAC アドレスや
  BSS の分析に役立つのではないか。IEEE のデータをダウンロードしてキャッシュする
  方式が既存シリーズと揃う」という提案
- IEEE のデータ源を実測して確認。5 ファイル・約 58,000 件・5.6 MB、
  同一スキーマ、`ETag`/`Last-Modified` あり、認証不要であることを検証済み
- 実測中に **ブラウザ偽装 UA が 418 で弾かれる**（素の UA なら 200）挙動を発見。
  制約として記録

**設計上の主要な指摘**

1. 単純な「先頭 6 桁引き」は誤答する。MA-L/MA-M/MA-S・IAB が同一 24bit
   プレフィクスを分割保有するため、36 → 28 → 24 の最長一致が必須
2. BSSID/MAC 分析で真に効くのはベンダー名より先に **アドレスのセマンティクス**。
   LAA（ランダム化 MAC）を「ベンダー不明」と返すと誤った端末同定に直行するため、
   「照会が無意味」と明示して返す設計を中核に据える

**決定事項（すべてユーザー承認済み）**

| 論点 | 決定 | 却下した案 |
|---|---|---|
| ツール名 | `mac-lookup` | `oui-lookup`（LAA/multicast 判定というコア機能が名前から外れる） |
| シリーズ配置 | cybersecurity-series | util-series（中立な資産情報ではあるが、用途と発見性を優先） |
| BSSID 群解析 | v0.2 以降 | v0.1 に含める（ヒューリスティックの精度検証コストが乗る） |
| 終了コード | 0/1/2 tri-state | 0/2 の二値 |
| ベンダー名逆引き | v0.1 に含める | v0.2 に回す |
| 登録住所の扱い | `--json` に原文のまま保持 | 国コードを抽出（書式不揃いで誤抽出リスク）／完全に破棄 |
