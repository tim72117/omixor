# 把 git commits 讀進 omixor:`omixor commits sync`

把「目前這個 git repo 的新 commit」送進 omixor,commit 訊息裡提到的 Issue(例如 `fixes ISS-12`)會自動連結。設計見 `design-commits.md`。
只送 commit metadata(sha、訊息、作者、時間、分支、父 commit),不送 diff 與檔案內容;不需要 GitHub / GitLab token,私有 repo 也行。

## 用法

```
omixor commits sync [--repo github:owner/name] [--org default] [--source cli|ci] [--dry-run] [--json] [--server 網址]
```

- 在 repo 目錄內執行(只讀本機 `git log`,不改 repo)。要先 `omixor login`(CI 用 token,見下)。
- `--repo`:沒給就從 `git remote get-url origin`(沒有 origin 用第一個 remote)推導:
  - `github.com` → `github:owner/name`
  - 主機名含 `gitlab` → `gitlab:group/sub/project`(巢狀群組照留)
  - 其他主機 → `owner/name`(沒有前綴)
  - 支援 `https://`、`ssh://`、scp 式(`git@host:owner/name.git`);去掉 `.git`、轉小寫。推導不出來會要求你加 `--repo`。
- `--source`:`cli`(預設);環境變數 `CI=true` 時預設 `ci`。
- `--dry-run`:只列出會送哪些 commit,什麼都不送(仍會讀伺服器上的同步位置)。
- `--json`:機器可讀摘要(`repo`、`sent`、`created`、`linked`、`unmatched`、`baseline`…)。
- 失敗時結束碼非 0(git 錯誤、伺服器錯誤;429 會印出多久後可重試)。

輸出:repo、送了幾筆、新建幾筆、建立的連結(Issue 與 fixes / mentions)、沒對到的 Issue 編號(unmatched,可能打錯)。

## 從哪裡開始(基準點規則)

| 伺服器的同步位置 | 本機狀況 | 做什麼 |
|---|---|---|
| 沒有(這個 repo 第一次) | — | **只送目前的 HEAD 一筆**當基準點,印 `baseline set at <sha7>; future runs send only new commits` |
| 有 | 那筆在本機、而且是 HEAD 的祖先 | 送 `<同步位置>..HEAD`(舊的在前,每批最多 100 筆);沒有新的印 `up to date` |
| 有 | 本機沒有那筆,或不是 HEAD 的祖先(rebase、force-push、別的 clone) | 不猜:只送 HEAD 當新的基準點,stderr 警告 |

一次最多送 500 筆(最新的 500 筆),超過的略過並警告。

**不支援補舊歷史(backfill)**:這個版本刻意只處理「從現在開始的新 commit」,沒有補歷史的旗標(2026-11-24 決定)。要讓舊 commit 對上 Issue,請等之後的版本。

頻率上限(每個使用者每小時 600 筆、120 次呼叫)超過會 429。

## git hook

`.git/hooks/post-commit`(或 `post-merge`;記得 `chmod +x`)。失敗不要擋住 git 操作:

```sh
#!/bin/sh
omixor commits sync >/dev/null 2>&1 || true
```

要看輸出就拿掉 `>/dev/null 2>&1`。

## CI

CI 需要一個 omixor token(使用者 token,`omx_` 開頭;角色憑證不能回報 commits),放在 CI 的 secret 裡,**不要寫進 repo**。以下的 `OMIXOR_TOKEN` 是你自己建立的 secret 名稱。
CLI 讀的是 `omixor login` 存下的登入檔;CI 上請先把 token 寫成登入檔(`OMIXOR_CREDENTIALS` 指向的檔案,格式 `{"server": "...", "token": "..."}`,權限 600),再執行 sync。

### GitHub Actions

```yaml
on: { push: { branches: [main] } }
jobs:
  omixor-commits:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with: { fetch-depth: 0 }   # 要有完整歷史,才找得到上次同步位置
      - name: Sync commits to omixor
        env:
          OMIXOR_SERVER: https://omixor.io
          OMIXOR_TOKEN: ${{ secrets.OMIXOR_TOKEN }}
          OMIXOR_CREDENTIALS: ${{ runner.temp }}/omixor-cred.json
        run: |
          umask 077
          printf '{"server":"%s","token":"%s"}' "$OMIXOR_SERVER" "$OMIXOR_TOKEN" > "$OMIXOR_CREDENTIALS"
          omixor commits sync --org default --source ci
```

### GitLab CI

```yaml
omixor-commits:
  stage: .post
  variables:
    GIT_DEPTH: "0"
    OMIXOR_SERVER: https://omixor.io
    OMIXOR_CREDENTIALS: $CI_PROJECT_DIR/.omixor-cred.json
  script:
    - umask 077
    - printf '{"server":"%s","token":"%s"}' "$OMIXOR_SERVER" "$OMIXOR_TOKEN" > "$OMIXOR_CREDENTIALS"
    - omixor commits sync --org default --source ci
  rules:
    - if: $CI_COMMIT_BRANCH == $CI_DEFAULT_BRANCH
```

`OMIXOR_TOKEN` 是 GitLab 專案的 CI/CD 變數(Masked);`.omixor-cred.json` 不要 commit(也不要當 artifact)。CI 的 checkout 常是 detached HEAD,這時送出的 `branch` 是空的。
