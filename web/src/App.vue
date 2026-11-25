<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, shallowRef, watch } from 'vue'
import Icon from './Icon.vue'
import TrendChart from './TrendChart.vue'
import {
  errorLabel, formatBytes, formatCount, formatLoad, formatPercent, formatTime,
  metrics, percent, statusLabels,
} from './format'

const overview = ref(null)
const history = shallowRef(null)
const trend = shallowRef(null)
const selected = ref('all')
const range = ref('24h')
const metric = ref(metrics[0])
const page = ref(1)
const activeSection = ref(location.hash === '#history' ? 'history' : 'overview')
const overviewError = ref('')
const historyError = ref(null)
const notice = ref('')
const refreshing = ref(false)
const historyPending = ref(null)
const ranges = [
  { key: '24h', label: '24 小时' },
  { key: '7d', label: '7 天' },
  { key: '30d', label: '30 天' },
]
const abortOnUnmount = new AbortController()
let historyRequest
let overviewRequest
let timer

const servers = computed(() => (overview.value?.servers || []).map((server, i) => ({
  ...server, colorIndex: i, sample: server.lastSuccess || server.latest,
})))
const names = computed(() => Object.fromEntries(servers.value.map(s => [s.id, s.name])))
const summary = computed(() => {
  const all = servers.value
  const running = all.filter(s => s.status === 'running').length
  const attention = all.filter(s => !['running', 'pending'].includes(s.status)).length
  const sampled = all.filter(s => s.lastSuccess || (s.latest && s.latest.status !== 'failed')).length
  const withTraffic = all.filter(s => s.sample?.trafficUsed != null)
  const traffic = withTraffic.reduce((total, s) => total + s.sample.trafficUsed, 0)
  return [
    { label: '监控实例', icon: 'server', color: 'violet', value: all.length, hint: 'BandwagonHost' },
    { label: '运行中', icon: 'pulse', color: 'mint', value: running, hint: `共 ${all.length} 台实例` },
    { label: '待关注', icon: 'alert', color: 'amber', value: attention, hint: sampled < all.length ? '仍有实例等待有效采样' : attention ? '查看实例状态' : '实例状态正常' },
    { label: '本期流量', icon: 'traffic', color: 'blue', value: withTraffic.length ? formatBytes(traffic) : '—',
      hint: withTraffic.length === all.length ? '所有实例的账期已用流量' : `${withTraffic.length} 台实例返回流量数据` },
  ]
})
const pageCount = computed(() => Math.max(1, Math.ceil((history.value?.total ?? 0) / 10)))
const historyBusy = computed(() => historyPending.value !== null)
const paginationDisabled = computed(() => historyBusy.value ||
  trend.value?.server !== selected.value || trend.value?.range !== range.value)
const intervalLabel = computed(() => {
  const seconds = overview.value?.pollIntervalSeconds
  if (!seconds) return '—'
  if (seconds % 3600 === 0) return seconds / 3600 + ' 小时'
  return seconds % 60 === 0 ? seconds / 60 + ' 分钟' : seconds + ' 秒'
})

async function request(url, options) {
  const response = await fetch(url, { signal: abortOnUnmount.signal, ...options })
  if (!response.ok) {
    const data = await response.json().catch(() => null)
    const error = new Error(data?.error || '无法读取监控服务，请稍后重试')
    error.retryAfter = Number(response.headers.get('Retry-After'))
    throw error
  }
  return response.json()
}

function loadOverview() {
  if (overviewRequest) return overviewRequest
  overviewRequest = request('/api/overview')
    .then(data => {
      const updated = overview.value?.updatedAt !== data.updatedAt
      overview.value = data
      overviewError.value = ''
      return updated
    })
    .catch(error => {
      if (!abortOnUnmount.signal.aborted) overviewError.value = error.message
      return false
    })
    .finally(() => { overviewRequest = undefined })
  return overviewRequest
}

async function loadHistory(updateTrend = true) {
  if (abortOnUnmount.signal.aborted) return
  historyRequest?.abort()
  const controller = new AbortController()
  historyRequest = controller
  const server = selected.value
  const timeRange = range.value
  const target = updateTrend || trend.value?.server !== server || trend.value?.range !== timeRange ? 'trend' : 'records'
  historyPending.value = target
  historyError.value = null
  const query = new URLSearchParams({ server, range: timeRange, page: page.value })
  try {
    const data = await request('/api/history?' + query, { signal: controller.signal })
    if (controller.signal.aborted) return
    history.value = data
    if (target === 'trend') {
      trend.value = {
        data, server, range: timeRange,
        servers: servers.value
          .filter(s => server === 'all' || s.id === server)
          .map(({ id, name, colorIndex }) => ({ id, name, colorIndex })),
      }
    }
  } catch (error) {
    if (!controller.signal.aborted) historyError.value = { target, message: error.message }
  } finally {
    if (historyRequest === controller) historyPending.value = null
  }
}

async function sync() {
  if (refreshing.value) return
  const updated = await loadOverview()
  if (overview.value && page.value === 1 && !historyBusy.value && (updated || !history.value || historyError.value?.target === 'trend')) {
    await loadHistory()
  }
}

async function refresh() {
  refreshing.value = true
  notice.value = ''
  try {
    await request('/api/refresh', { method: 'POST', headers: { 'X-Monitor-Refresh': '1' } })
    await overviewRequest
    await loadOverview()
    await loadHistory()
  } catch (error) {
    if (!abortOnUnmount.signal.aborted) {
      notice.value = error.retryAfter ? `请在 ${error.retryAfter} 秒后重新采集。` : error.message
      await loadOverview()
    }
  } finally {
    refreshing.value = false
  }
}

function changeServer(id) {
  selected.value = id
  page.value = 1
}

function setRange(key) {
  range.value = key
  page.value = 1
}

function changePage(next) {
  if (page.value === next) {
    loadHistory(false)
  } else {
    page.value = next
  }
}

function syncSection() {
  activeSection.value = location.hash === '#history' ? 'history' : 'overview'
}

function displayUsage(sample) {
  return [
    { label: '内存', color: 'memory', used: sample?.memoryUsed, total: sample?.memoryTotal },
    { label: '磁盘映射', color: 'disk', used: sample?.diskUsed, total: sample?.diskTotal },
    { label: '账期流量', color: 'traffic', used: sample?.trafficUsed, total: sample?.trafficCap },
  ].map(usage => ({ ...usage, value: percent(usage.used, usage.total) }))
}

watch([selected, range, page], ([server, timeRange], [oldServer, oldRange]) => {
  loadHistory(server !== oldServer || timeRange !== oldRange)
})
onMounted(async () => {
  window.addEventListener('hashchange', syncSection)
  timer = window.setInterval(sync, 30_000)
  await sync()
  if (location.hash === '#history') {
    await nextTick()
    document.getElementById('history')?.scrollIntoView()
  }
})
onBeforeUnmount(() => {
  window.removeEventListener('hashchange', syncSection)
  window.clearInterval(timer)
  abortOnUnmount.abort()
  historyRequest?.abort()
})
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar">
      <a href="#overview" class="brand" aria-label="VPS Monitor 总览">
        <span class="brand-icon"><Icon name="monitor" :size="23" /></span>
        <span>VPS<span class="brand-accent"> Monitor</span></span>
      </a>
      <nav aria-label="主导航" class="sidebar-nav">
        <div class="nav-group">
          <span class="nav-label">监控中心</span>
          <a href="#overview" class="nav-link" :aria-current="activeSection === 'overview' ? 'location' : undefined">
            <Icon name="overview" :size="18" />总览
          </a>
          <a href="#history" class="nav-link" :aria-current="activeSection === 'history' && selected === 'all' ? 'location' : undefined" @click="changeServer('all')">
            <Icon name="history" :size="18" />历史记录
          </a>
        </div>
        <div v-if="servers.length" class="nav-group server-nav">
          <span class="nav-label">服务器</span>
          <a v-for="server in servers" :key="server.id" href="#history" class="nav-link instance-link"
            :aria-current="activeSection === 'history' && selected === server.id ? 'location' : undefined"
            @click="changeServer(server.id)">
            <span class="nav-dot" :class="'dot-' + server.status"></span><span class="min-w-0 break-words">{{ server.name }}</span>
          </a>
        </div>
      </nav>
      <div class="sidebar-footer">
        <div class="provider-icon"><Icon name="layers" :size="18" /></div>
        <div class="min-w-0">
          <p class="text-[0.875rem] font-semibold text-ink">KiwiVM API</p>
          <p class="text-[0.75rem] text-muted">每 {{ intervalLabel }}采集</p>
        </div>
      </div>
    </aside>

    <div class="main-shell">
      <header class="topbar">
        <div class="topbar-inner">
          <a href="#overview" class="brand mobile-brand" aria-label="VPS Monitor 总览">
            <span class="brand-icon"><Icon name="monitor" :size="21" /></span>
            <span>VPS<span class="brand-accent"> Monitor</span></span>
          </a>
          <span class="breadcrumb">监控中心</span>
          <div class="flex flex-wrap items-center gap-x-4 gap-y-2">
            <span v-if="overview?.demo" class="demo-pill"><span></span>演示模式</span>
            <span v-if="overview" class="top-timestamp">
              <Icon name="clock" :size="15" />
              <span>最后采样 <time v-if="overview.updatedAt" class="font-mono text-soft" :datetime="overview.updatedAt">{{ formatTime(overview.updatedAt, 'clock') }}</time><span v-else>—</span></span>
            </span>
          </div>
        </div>
      </header>

      <main id="overview" class="dashboard">
        <div class="page-heading">
          <div>
            <h1>服务器监控</h1>
            <p class="mt-2 text-muted">查看 VPS 运行状态、资源用量和历史采样。</p>
            <p v-if="overview" class="mobile-timestamp">
              <Icon name="clock" :size="15" />
              <span>最后采样 <time v-if="overview.updatedAt" class="font-mono text-soft" :datetime="overview.updatedAt">{{ formatTime(overview.updatedAt, 'clock') }}</time><span v-else>—</span></span>
            </p>
          </div>
          <button type="button" class="primary-button" :disabled="refreshing || !overview" @click="refresh">
            <Icon name="refresh" :size="17" :class="{ 'refreshing': refreshing }" />
            {{ refreshing ? '采集中…' : '立即采集' }}
          </button>
        </div>

        <template v-if="!overview">
          <p v-if="overviewError" class="message error-message" role="alert">{{ overviewError }}</p>
          <p v-else class="message" role="status">正在读取监控数据…</p>
        </template>

        <template v-if="overview">
          <div v-if="overviewError || notice" class="dashboard-notices">
            <div v-if="overviewError" class="message error-message">
              <span role="alert">{{ overviewError }}</span>
              <button type="button" class="detail-link" aria-label="关闭状态读取提示" @click="overviewError = ''">关闭</button>
            </div>
            <div v-if="notice" class="message">
              <span role="status">{{ notice }}</span>
              <button type="button" class="detail-link" aria-label="关闭采集提示" @click="notice = ''">关闭</button>
            </div>
          </div>
          <div class="stats-grid" aria-label="监控摘要">
            <article v-for="stat in summary" :key="stat.label" class="panel stat">
              <div class="flex items-center gap-2.5">
                <span class="stat-icon" :class="'tint-' + stat.color"><Icon :name="stat.icon" :size="18" /></span>
                <h2 class="text-sm font-medium text-soft">{{ stat.label }}</h2>
              </div>
              <p class="stat-value">{{ stat.value }}</p>
              <p class="text-[0.8125rem] text-muted">{{ stat.hint }}</p>
            </article>
          </div>

          <section class="block-section" aria-labelledby="instances-heading">
            <div class="section-heading">
              <h2 id="instances-heading">实例状态</h2>
              <span class="text-sm text-muted">{{ servers.length }} 台实例</span>
            </div>
            <div class="instances-grid">
              <article v-for="server in servers" :key="server.id" class="panel instance">
                <div class="instance-heading">
                  <div class="flex min-w-0 items-center gap-3">
                    <span class="instance-icon"><Icon name="server" :size="23" /></span>
                    <div class="min-w-0">
                      <h3 class="break-words text-base font-semibold text-ink">{{ server.name }}</h3>
                      <p class="mt-0.5 break-words font-mono text-[0.8125rem] text-muted">{{ server.sample?.hostname || server.id }}</p>
                    </div>
                  </div>
                  <span class="status-pill" :class="'status-' + server.status">
                    <span class="nav-dot" :class="'dot-' + server.status"></span>{{ statusLabels[server.status] }}
                  </span>
                </div>

                <div class="instance-meta">
                  <span v-if="server.sample?.location" class="flex min-w-0 items-center gap-1.5 break-words">
                    <Icon name="pin" :size="15" />{{ server.sample.location }}
                  </span>
                  <span v-if="server.sample?.os" class="os-tag break-words">{{ server.sample.os }}</span>
                  <span v-if="server.sample?.load != null" class="ml-auto flex items-center gap-1.5">
                    <Icon name="pulse" :size="15" />负载 <strong class="font-mono font-medium text-soft">{{ formatLoad(server.sample.load) }}</strong>
                  </span>
                </div>

                <p v-if="server.status === 'failed'" class="mb-3 text-sm text-amber-300">
                  {{ errorLabel(server.latest.error) }}<template v-if="server.lastSuccess"> · 上次有效采样 {{ formatTime(server.lastSuccess.at) }}</template>
                </p>
                <p v-else-if="server.status === 'pending'" class="mb-3 text-sm text-muted">正在等待首次采样。</p>

                <div class="usage-grid">
                  <div v-for="usage in displayUsage(server.sample)" :key="usage.label" class="min-w-0">
                    <div class="flex flex-wrap items-baseline justify-between gap-x-2">
                      <span class="text-sm text-soft">{{ usage.label }}</span>
                      <strong class="font-mono text-[0.8125rem] font-semibold text-ink">{{ formatPercent(usage.value) }}</strong>
                    </div>
                    <div class="progress-track" aria-hidden="true">
                      <div v-if="usage.value != null" class="progress-fill" :class="usage.color" :style="{ width: Math.min(100, usage.value) + '%' }"></div>
                    </div>
                    <p class="break-words text-[0.75rem] text-muted">{{ formatBytes(usage.used) }} / {{ formatBytes(usage.total) }}</p>
                  </div>
                </div>
                <div class="instance-footer">
                  <span v-if="server.sample?.resetAt" class="flex items-center gap-1.5 text-[0.8125rem] text-muted">
                    <Icon name="calendar" :size="15" />流量重置 {{ formatTime(server.sample.resetAt, 'date') }}
                  </span>
                  <a href="#history" class="detail-link" @click="changeServer(server.id)">查看历史</a>
                </div>
              </article>
            </div>
          </section>

          <section id="history" class="block-section history-section" aria-labelledby="history-heading">
            <div class="section-heading">
              <h2 id="history-heading">历史趋势</h2>
              <label class="select-wrap">
                <span class="sr-only">筛选实例</span>
                <select :value="selected" @change="changeServer($event.target.value)">
                  <option value="all">全部实例</option>
                  <option v-for="server in servers" :key="server.id" :value="server.id">{{ server.name }}</option>
                </select>
                <Icon name="chevron" :size="16" />
              </label>
            </div>
            <div class="panel chart-panel">
              <div class="chart-controls">
                <div class="segments" role="group" aria-label="监控指标">
                  <button v-for="item in metrics" :key="item.key" type="button" :aria-pressed="metric.key === item.key" @click="metric = item">{{ item.label }}</button>
                </div>
                <div class="segments range-controls" role="group" aria-label="时间范围">
                  <button v-for="item in ranges" :key="item.key" type="button" :aria-pressed="range === item.key" @click="setRange(item.key)">{{ item.label }}</button>
                </div>
              </div>
              <div class="history-feedback">
                <template v-if="historyError?.target === 'trend'">
                  <span role="alert">{{ historyError.message }}<template v-if="history"> · 显示上次结果</template></span>
                  <button type="button" class="detail-link" @click="loadHistory()">重试</button>
                </template>
                <span v-else role="status">{{ historyPending === 'trend' ? (history ? '正在更新，暂显示上次结果' : '正在读取采样记录…') : '' }}</span>
              </div>
              <div :aria-busy="historyPending === 'trend'">
                <TrendChart :history="trend?.data" :servers="trend?.servers" :metric="metric" :range="trend?.range" />
              </div>
            </div>

            <div class="section-heading records-heading">
              <h2 id="records-heading">采样记录</h2>
              <span v-if="history" class="text-sm text-muted">共 {{ formatCount(history.total) }} 条</span>
            </div>
            <div class="panel records-panel" :aria-busy="historyBusy">
              <div v-if="history?.records.length" class="table-scroll" tabindex="0" role="region" aria-labelledby="records-heading">
                <table>
                  <thead>
                    <tr>
                      <th scope="col">采样时间</th><th scope="col">实例</th><th scope="col">状态</th>
                      <th scope="col">内存</th><th scope="col">系统负载</th><th scope="col">磁盘映射</th><th scope="col">账期流量</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="record in history.records" :key="record.serverId + record.at">
                      <th scope="row"><time class="font-mono" :datetime="record.at">{{ formatTime(record.at) }}</time></th>
                      <td>{{ names[record.serverId] || record.serverId }}</td>
                      <td>
                        <span class="status-pill" :class="'status-' + record.status" :title="record.error ? errorLabel(record.error) : undefined">
                          <span class="nav-dot" :class="'dot-' + record.status"></span>{{ statusLabels[record.status] }}
                        </span>
                      </td>
                      <td class="font-mono">{{ formatPercent(percent(record.memoryUsed, record.memoryTotal)) }}</td>
                      <td class="font-mono">{{ formatLoad(record.load) }}</td>
                      <td class="font-mono">{{ formatPercent(percent(record.diskUsed, record.diskTotal)) }}</td>
                      <td class="font-mono">{{ formatPercent(percent(record.trafficUsed, record.trafficCap)) }}</td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <p v-else class="p-5 text-sm text-muted">{{ historyBusy ? '正在读取采样记录…' : historyError ? '采样记录暂不可用' : '该时段暂无采样记录' }}</p>
              <div v-if="history?.total > 10" class="pagination">
                <div class="flex items-baseline gap-2">
                  <span role="status" class="text-sm" :class="historyError?.target === 'records' ? 'text-rose-200' : 'text-muted'">
                    <span v-if="historyPending === 'records'" aria-hidden="true">正在翻页…</span>
                    <template v-else-if="historyError?.target === 'records'">翻页失败</template>
                    <template v-else>第 {{ history.page }} / {{ pageCount }} 页</template>
                  </span>
                  <button v-if="historyError?.target === 'records'" type="button" @click="loadHistory(false)">重试</button>
                </div>
                <div class="flex gap-2">
                  <button type="button" :disabled="history.page <= 1 || paginationDisabled" @click="changePage(history.page - 1)">上一页</button>
                  <button type="button" :disabled="history.page >= pageCount || paginationDisabled" @click="changePage(history.page + 1)">下一页</button>
                </div>
              </div>
            </div>
          </section>
        </template>
      </main>
    </div>
  </div>
</template>
