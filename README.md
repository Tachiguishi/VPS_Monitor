# VPS Monitor

用 Go、Vue 3 和 Tailwind CSS 实现的自托管 VPS 看板。Go 服务定时读取 BandwagonHost 的 KiwiVM API，将每台 VPS 的采样写入本地 SQLite。浏览器关闭后，后台仍会采集。

- 同时查看多个实例的运行状态、机房、系统、负载、内存、磁盘映射和当前账期流量。
- 按实例和最近 24 小时、7 天或 30 天筛选历史趋势，分页查看原始采样。
- 每次采集同时查询两台机器。某一台采集失败时，会记录失败及时间，实例卡片会标明上一条有效采样。
- 支持立即采集。同一服务两次采集至少间隔 1 分钟。

## 运行演示

需要 Go 1.25 或以上、Node.js 22.12 或以上和 npm。在项目根目录执行：

```sh
npm --prefix web ci
npm --prefix web run build
go run -buildvcs=false . -demo
```

打开 [http://127.0.0.1:8080](http://127.0.0.1:8080)。演示模式在内存中生成两台服务器最近 7 天的历史记录，可直接操作看板。

## 接入两台服务器

1. 分别进入两台 VPS 的 KiwiVM 控制面板，在 **API / REST API** 页面取得各自的 VEID 和 API Key。
2. 如果还没有 `config.json`，执行 `cp config.example.json config.json`。修改两个实例的 `name` 和 `veid`，并让各自的 `apiKeyEnv` 与 `.env` 中的变量名一致。
3. 在项目目录执行 `cp -n .env.example .env`，填写 `.env` 中的 `KIWIVM_API_KEY_1` 和 `KIWIVM_API_KEY_2`。服务启动时会自动读取当前工作目录的 `.env`。终端或服务管理器中已设置的同名变量优先。`.env` 已加入 Git 忽略规则。
4. 在项目目录启动服务：

   ```sh
   go run -buildvcs=false . -config config.json
   ```

服务启动后立即开始首轮采集，之后默认每 5 分钟采集一次。页面每 30 秒读取已保存的最新状态。默认数据库为 `data/monitor.db`，保留最近 90 天的样本。可在配置中调整地址、数据库路径、采集间隔和保留天数。移动部署目录时请保留数据库。

| 配置项 | 用途 |
| --- | --- |
| `listen` | HTTP 监听地址，默认 `127.0.0.1:8080` |
| `database` | SQLite 数据库路径，相对路径基于当前工作目录 |
| `pollIntervalSeconds` | 自动采集间隔，范围为 60–86400 秒 |
| `retentionDays` | 样本保留天数，范围为 1–3650 天 |
| `servers` | 监控实例列表，每台使用独立的 VEID 和 API Key |

## 自托管

Go 进程同时提供网页和 API。部署时构建前端，再从项目目录启动二进制：

```sh
npm --prefix web ci
npm --prefix web run build
mkdir -p bin
go build -buildvcs=false -o bin/vps-monitor .
./bin/vps-monitor -config config.json
```

可由 systemd 等服务管理器常驻运行，将工作目录设为项目目录，并在 `.env` 或服务环境变量中提供密钥。对外访问时，用 HTTPS 反向代理转发到回环地址，并同时设置 `MONITOR_USER` 和 `MONITOR_PASSWORD`，启用 HTTP Basic Auth。默认监听回环地址，可通过 SSH 本地端口转发访问：

```sh
ssh -N -L 8080:127.0.0.1:8080 user@monitor-host
```

API Key 只由后端读取。实例采集使用 KiwiVM 的 `getLiveServiceInfo`。系统负载取 1 分钟平均值，磁盘用量指 KVM 磁盘映射占用。部分实例可能缺少来宾内存或负载数据，界面将显示 `—`。账期流量按 API 的机房系数换算，展示当前账期累计值，流量重置时曲线会回落。

## 开发

后端使用演示模式运行，另开终端运行 `npm --prefix web run dev`。Vite 默认将 `/api` 转发到 `127.0.0.1:8080`，可用 `MONITOR_API_URL` 修改。

```sh
go test ./...
npm --prefix web run build
```
