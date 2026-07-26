# mac-lookup

MAC アドレス／BSSID から製造元を引く。その前に **そのアドレスが何者なのか**
を判定する。IEEE Registration Authority の公開レジストリをローカルにキャッシュ
してオフラインで応答する CLI 兼 MCP サーバー。

[`asn-lookup`](https://github.com/nlink-jp/asn-lookup)（IP → AS/国）、
[`tor-exit-lookup`](https://github.com/nlink-jp/tor-exit-lookup)（オフライン
membership 判定）に対する L2 側の姉妹品。

## なぜ作るのか

ベンダー表を引くだけでは誤答する。先に片付けるべきことが 3 つある。

- **ランダム化 MAC に製造元は存在しない。** 現代のスマートフォンはネットワーク
  ごとに Locally Administered Address を使い分ける。仮想 NIC やコンテナも同様。
  これを「ベンダー不明」と返すと、そもそも登録されていない端末を探し続ける
  ことになる。mac-lookup は **「Locally Administered — ベンダー照会は該当しない」**
  と返す。
- **先頭 6 桁では足りない。** IEEE は 24bit プレフィクスを 28bit(MA-M) や
  36bit(MA-S/IAB) に分割して割り当てるため、1 つの OUI を複数ベンダーが共有する。
  mac-lookup は 36 → 28 → 24 の最長一致で照合する。
- **IEEE 自身が保有するブロックがある。** MA-L の約 429 行は登録者が
  「IEEE Registration Authority」であり、これは細分化のために留保された
  ブロックであって製造元ではない。mac-lookup は **「OUI 分割済み」** と返し、
  ベンダー名としては決して出さない。

すべてローカルキャッシュから応答するため、調査対象が観測できるネットワークに
一切触れない。

## インストール

```bash
brew install nlink-jp/tap/mac-lookup
```

ソースからビルドする場合（Go 1.25+、外部依存なし）:

```bash
make build
```

バイナリは `dist/mac-lookup` に出力される。

## 使い方

```bash
mac-lookup update                        # IEEE レジストリを初回ダウンロード
mac-lookup lookup 8C:1F:64:AF:A0:01      # アドレスを解決
mac-lookup lookup --json < captured.txt  # stdin バッチ、JSON Lines 出力
mac-lookup search Apple                  # ベンダー名 → 割当プレフィクス
mac-lookup status                        # キャッシュの鮮度と件数
mac-lookup mcp                           # MCP サーバーとして起動 (stdio)
```

受理する表記: `00:11:22:33:44:55`、`00-11-22-33-44-55`、`001122334455`、
Cisco 形式の `0011.2233.4455`、および 24/28/36bit の部分プレフィクス
（`00:11:22`、`8C:1F:64:AF:A`）。

```
$ mac-lookup lookup 8C:1F:64:AF:A0:01 8C:1F:64:00:00:01 DA:A1:19:12:34:56 00:00:5E:00:01:2A FF:FF:FF:FF:FF:FF
8C:1F:64:AF:A0:01  DATA ELECTRONIC DEVICES, INC  [MA-S /36]
8C:1F:64:00:00:01  Suzhou Xingxiangyi Precision Manufacturing Co.,Ltd.  [MA-S /36]
DA:A1:19:12:34:56  Google, Inc.  [CID /24]  (locally administered)
00:00:5E:00:01:2A  ICANN, IANA Department  [MA-L /24]  (VRRP (IPv4, RFC 5798))
FF:FF:FF:FF:FF:FF  (broadcast — Broadcast)
```

このうち 2 行が本ツールの存在理由である。`8C:1F:64` は IEEE 保有ブロックなので、
ベンダー名は OUI ではなくその内側の 36bit 割当から取れている。`DA:A1:19` は
Locally Administered で、CID レジストリがプレフィクスの割当元として Google を
示すが、**このアドレスは安定した端末識別子ではなく、ネットワークをまたいだ
相関に使ってはならない**。行末にそう明示しているのはそのためである。

### 終了コード

`lookup` は **単一アドレス + text 出力時のみ** grep 風の終了コードを返す。

| Code | 意味 |
|---|---|
| `0` | ベンダー名が確定した |
| `1` | ベンダー名が出ない（未割り当て / Locally Administered / multicast / OUI 分割済み / `Private` 登録） |
| `2` | エラー |

```bash
if mac-lookup lookup "$mac"; then echo "ベンダー既知"; fi
```

複数アドレス・stdin・`--json` はバッチモードに切り替わり、結果は stdout に、
終了コードはエラーの有無のみ（`0`/`2`）になる。

## 設定

任意。[`config.example.toml`](config.example.toml) を
`~/.config/mac-lookup/config.toml` にコピーする。すべての項目に
`MAC_LOOKUP_*` 環境変数の上書きがある。

**認証情報は存在しない。** IEEE のレジストリファイルは公開されているため、
設定・ログ出力・漏洩の対象になるトークンや API キーがそもそもない。

キャッシュは TTL（既定 24 時間、下限 6 時間）を超えると自動で再取得する。
IEEE 側の再生成が 1 日 1 回程度のため、それ以上の頻度で叩かない。
`--no-update` または `[ieee] auto_update = false` で無効化できる。

## MCP サーバー

`mac-lookup mcp` は stdio 上の JSON-RPC 2.0 で `lookup_mac`、`search_vendor`、
`db_status`、`update_db`、`get_usage` を公開する。まず `get_usage` を呼ぶこと
——ツールリファレンスとエラー復旧表が返る。

`search_vendor` は file-mediated。大手ベンダーは数百のプレフィクスを保有する
ため、結果は呼び出し側の `workspace_root` 配下に書き出し、`matches_file` の
パスを返す。

### JSON 出力

`--json` はアドレスごとに 1 オブジェクトを出力する。**`vendor` より先に
`vendor_lookup_applicable` を読むこと。** これが `false` のとき、`vendor` が
空なのは「照会に失敗した」のではなく「探すべきものが存在しない」ことを意味する。
どちらであるかは `note` に文章で入る。登録住所は IEEE の原文のまま保持し、
フリーテキストであるため国コードの抽出は行わない。

## データ

IEEE Registration Authority の公開レジストリ — MA-L / MA-M / MA-S / IAB / CID
(<https://standards.ieee.org/products-programs/regauth/>)。2026-07-26 時点で
58,212 件。認証不要。

レジストリデータは実行時にダウンロードしてローカルにキャッシュする。
本ツールに同梱・再配布はしない。

## ライセンス

MIT。[LICENSE](LICENSE) を参照。
