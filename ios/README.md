# omixor iOS

omixor 的 iOS 版(第一版:配對登入、對話、Issue、派發)。

## 先產生 Xcode 專案

`Omixor.xcodeproj` 由 [xcodegen](https://github.com/yonaskolb/XcodeGen) 從 `project.yml` 產生,**不進版控**(`.gitignore` 排除 `ios/*.xcodeproj/`)。
clone 下來或改了 `project.yml`(新增檔案、改設定)之後,先執行:

```
brew install xcodegen      # 第一次
cd ios && xcodegen generate
```

要改專案設定請改 `project.yml`,不要直接改 `.xcodeproj`(下次產生會被蓋掉)。

## 建置與測試

```
cd ios
xcodebuild -scheme Omixor -destination 'platform=iOS Simulator,name=iPhone 17 Pro' build test
```
