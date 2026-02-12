# PDF 图像翻译工具

Go 后端负责拆分 PDF、提取页图、调用多种大模型翻译，并生成 TXT / PDF；Vue3 前端提供上传、批量翻译、任务恢复与 AI 排版等交互。

## 功能概览
- 拆分 PDF 为单页图片并并发翻译，支持翻译范围与批量大小的配置。
- 支持 OpenAI / Gemini / Anthropic / 自定义 OpenAI 兼容 API，并可在前端维护多提供商与模型列表。
- 每页支持人工校正、重新翻译、失败重试；任务可暂停/继续、刷新后恢复。
- TXT 导出提供原版与 AI 排版版本，PDF 导出基于逐页译文；AI 排版调用大模型对合并文本分块处理，带进度显示与日志。

## 概览
https://photo.459122.xyz/i/10c268d64288857abbcc4d5c4f93a662.mp4

## 仓库结构

| 目录 | 说明 |
| --- | --- |
| `pdftool` | Go 后端服务（API、任务管理、翻译/排版调度）。|
| `pdftool-frontend` | Vue3 + Vite 单页前端。|
| `storage/pdf_tool` | 运行期生成的任务、图片、文本等资源（可在配置中调整）。|

## 后端

### 运行

```bash
cd pdftool
go mod tidy  # 首次运行可执行
go run ./cmd/server
# 或构建可执行文件
go build -o bin/pdftool ./cmd/server
./bin/pdftool
```

服务启动后会监听默认 `http://localhost:8090`，并通过 `/api/pdf/...` 暴露接口和 `/pdf-data/...` 暴露静态资源。

### 环境变量（可选）
<details>
<summary>点击展开高级配置选项（通常不需要修改）</summary>

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `PDFTOOL_LISTEN_ADDR` | `:8090` | HTTP 监听地址。|
| `PDFTOOL_STORAGE_DIR` | `storage/pdf_tool` | 任务存储目录。|
| `PDFTOOL_STATIC_PREFIX` | `/pdf-data` | 静态文件访问前缀。|
| `OPENAI_BASE_URL` | `https://api.openai.com/v1` | 默认提供商 API。|
| `OPENAI_API_KEY` | 无 | 默认 Key，前端也可覆盖。|
| `OPENAI_MODEL` | 无 | 默认模型。|
| `PDFTOOL_FONT_PATH` | 无 | 生成 PDF 时使用的字体（如不设置则使用内置字体）。|
| `PDFTOOL_MAX_WORKERS` | `4` | 翻译并发上限。|
| `PDFTOOL_TRANSLATION_TIMEOUT` | `300` | API 请求超时（秒）。|

</details>

## 前端

### 运行

```bash
cd pdftool-frontend
npm install
npm run dev --host 0.0.0.0 --port 5173
# 生产构建
npm run build
```

前端默认使用 `http://localhost:8090/api/pdf` 作为后端基地址，可在设置面板的「后端 API Base」中修改。部署静态资源时，将 `dist/` 内容放置于任意静态服务器即可。

### 使用流程
1. 打开前端，进入右上角「设置」配置至少一个提供商（填写 API Base、Key、模型，支持从提供商 API 获取模型列表并测试连接）。
2. 在页面顶部选择当前提供商与模型，确认后端地址正确。
3. 上传 PDF（支持拖拽），或输入任务 ID 恢复历史任务。
4. 通过页面上方的设置选择「每批处理页数」「翻译范围」等参数，使用工具栏执行批量翻译 / 重新翻译 / 重试失败。
5. 若需要 AI 排版，点击「AI 排版校对」，等待进度完成后可导出 AI 排版 TXT；原版 TXT 与 PDF 导出按钮位于同一区域。

## 日志与数据
- 后端默认将翻译、排版的请求和响应摘要打印到标准输出，包含每页编号以及错误详情，便于排查。
- 所有任务文件保存在 `PDFTOOL_STORAGE_DIR/<task-id>/` 中，包括原始 PDF、渲染图片、逐页 TXT、合并文件、AI 排版结果及分块输入，方便线下检查。

