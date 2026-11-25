const clock = new Intl.DateTimeFormat('zh-CN', {
  hour: '2-digit', minute: '2-digit', second: '2-digit', hourCycle: 'h23',
})
const fullDate = new Intl.DateTimeFormat('zh-CN', {
  month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit',
  second: '2-digit', hourCycle: 'h23',
})
const date = new Intl.DateTimeFormat('zh-CN', { month: 'long', day: 'numeric' })
const decimal = new Intl.NumberFormat('zh-CN', { maximumFractionDigits: 1 })
const count = new Intl.NumberFormat('zh-CN')

export const statusLabels = {
  running: '运行中',
  stopped: '已停止',
  starting: '启动中',
  suspended: '已暂停',
  pending: '等待采集',
  failed: '采集失败',
  unknown: '状态未知',
}

export const metrics = [
  { key: 'memory', label: '内存', unit: '%' },
  { key: 'load', label: '系统负载', unit: '' },
  { key: 'disk', label: '磁盘', unit: '%' },
  { key: 'traffic', label: '账期流量', unit: '%' },
]

export function formatTime(value, variant = 'full') {
  return value ? ({ clock, full: fullDate, date })[variant].format(new Date(value)) : '—'
}

export function formatBytes(value) {
  if (value == null) return '—'
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${decimal.format(value)} ${units[unit]}`
}

export function formatCount(value) {
  return count.format(value)
}

export function percent(used, total) {
  return used != null && total > 0 ? (used / total) * 100 : null
}

export function formatPercent(value) {
  return value == null ? '—' : `${Math.round(value)}%`
}

export function formatLoad(value) {
  return value == null ? '—' : value.toFixed(2)
}

export function errorLabel(error) {
  if (error === 'connection') return '无法连接 KiwiVM 或请求超时'
  if (error === 'invalid_response') return 'KiwiVM 响应格式异常'
  if (error?.startsWith('kiwivm_')) return `KiwiVM 错误 ${error.slice(7)}`
  if (error?.startsWith('http_')) return `KiwiVM 返回 HTTP ${error.slice(5)}`
  return '采集失败'
}
