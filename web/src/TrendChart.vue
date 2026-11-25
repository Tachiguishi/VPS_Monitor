<script setup>
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useTemplateRef, watch } from 'vue'
import { formatLoad, formatPercent, formatTime } from './format'
import Icon from './Icon.vue'

const props = defineProps({
  history: Object,
  servers: { type: Array, required: true },
  metric: { type: Object, required: true },
  range: String,
})
const panel = useTemplateRef('panel')
const chart = useTemplateRef('chart')
const tooltip = ref(null)
const colors = ['#a78bfa', '#5adac1', '#f5b67d', '#81aaff', '#f9a8ce']
const count = computed(() => props.servers.reduce((total, server) =>
  total + (props.history?.series?.[server.id] || []).filter(p => p[props.metric.key] != null).length, 0))
const shortTime = new Intl.DateTimeFormat('zh-CN', { hour: '2-digit', minute: '2-digit', hourCycle: 'h23' })
const shortDate = new Intl.DateTimeFormat('zh-CN', { month: 'numeric', day: 'numeric' })
let observer
let frame
let geometry

function seriesFor(server) {
  return props.history?.series?.[server.id] || []
}

function colorFor(server) {
  return colors[server.colorIndex % colors.length]
}

function timeLabel(timestamp) {
  return props.range === '24h' ? shortTime.format(timestamp) : shortDate.format(timestamp)
}

function draw() {
  const canvas = chart.value
  if (!canvas || !props.history) return
  const width = canvas.clientWidth
  const height = canvas.clientHeight
  if (!width || !height) return

  const dpr = window.devicePixelRatio || 1
  canvas.width = Math.round(width * dpr)
  canvas.height = Math.round(height * dpr)
  const ctx = canvas.getContext('2d')
  if (!ctx) return
  ctx.scale(canvas.width / width, canvas.height / height)

  const { metric, history } = props
  const start = Date.parse(history.from)
  const end = Date.parse(history.to)
  const sections = width < 470 ? 4 : 6
  const top = 12
  const right = width - 12
  const bottom = height - 30
  const left = 47
  let maxValue = metric.unit ? 100 : 1
  for (const server of props.servers) {
    for (const point of seriesFor(server)) {
      maxValue = Math.max(maxValue, point[metric.key] ?? 0)
    }
  }
  const limit = Math.ceil(maxValue / (metric.unit ? 25 : .25)) * (metric.unit ? 25 : .25)
  const x = at => left + ((at - start) / (end - start)) * (right - left)
  const y = value => bottom - (value / limit) * (bottom - top)
  geometry = { start, end, left, right, top, bottom, width }

  ctx.font = '12px ui-monospace, SFMono-Regular, Consolas, monospace'
  ctx.fillStyle = '#aaa5bc'
  for (let i = 0; i <= 4; i++) {
    const value = limit * i / 4
    const row = y(value)
    ctx.strokeStyle = '#353143'
    ctx.setLineDash([3, 5])
    ctx.beginPath()
    ctx.moveTo(left, row)
    ctx.lineTo(right, row)
    ctx.stroke()
    ctx.textBaseline = 'middle'
    ctx.textAlign = 'right'
    ctx.fillText(metric.unit ? Math.round(value) + '%' : value.toFixed(2), left - 10, row)
  }
  ctx.setLineDash([])
  ctx.textBaseline = 'bottom'
  for (let i = 0; i <= sections; i++) {
    const time = start + (end - start) * i / sections
    ctx.textAlign = i === 0 ? 'left' : i === sections ? 'right' : 'center'
    const col = x(time)
    ctx.fillText(timeLabel(time), col, height - 3)
    if (i === 0 || i === sections) continue
    ctx.strokeStyle = '#2b2738'
    ctx.beginPath()
    ctx.moveTo(col, top)
    ctx.lineTo(col, bottom)
    ctx.stroke()
  }

  for (const server of props.servers) {
    const color = colorFor(server)
    let segment = []
    const paint = () => {
      if (segment.length === 0) return
      if (segment.length === 1) {
        ctx.fillStyle = color
        ctx.beginPath()
        ctx.arc(segment[0].x, segment[0].y, 2.75, 0, 2 * Math.PI)
        ctx.fill()
        return
      }
      const points = new Path2D()
      points.moveTo(segment[0].x, segment[0].y)
      for (const point of segment.slice(1)) points.lineTo(point.x, point.y)
      const area = new Path2D(points)
      area.lineTo(segment.at(-1).x, bottom)
      area.lineTo(segment[0].x, bottom)
      area.closePath()
      const gradient = ctx.createLinearGradient(0, top, 0, bottom)
      gradient.addColorStop(0, color + '40')
      gradient.addColorStop(1, color + '03')
      ctx.fillStyle = gradient
      ctx.fill(area)
      ctx.lineWidth = 2
      ctx.lineJoin = 'round'
      ctx.strokeStyle = color
      ctx.stroke(points)
    }

    for (const point of seriesFor(server)) {
      const time = Date.parse(point.at)
      const value = point[metric.key]
      if (value == null || (segment.length && time - segment.at(-1).time > history.stepSeconds * 2100)) {
        paint()
        segment = []
      }
      if (value != null) segment.push({ x: x(time), y: y(value), time })
    }
    paint()
  }
}

function scheduleDraw() {
  cancelAnimationFrame(frame)
  frame = requestAnimationFrame(draw)
}

function showTooltip(event) {
  const plot = geometry
  if (!plot) return
  const rect = chart.value.getBoundingClientRect()
  const x = event.clientX - rect.left
  const y = event.clientY - rect.top
  if (x < plot.left || x > plot.right || y < plot.top || y > plot.bottom) {
    tooltip.value = null
    return
  }
  const time = plot.start + (plot.end - plot.start) * (x - plot.left) / (plot.right - plot.left)
  const rows = []
  for (const server of props.servers) {
    const points = seriesFor(server)
    let closest = null
    let distance = Infinity
    for (const point of points) {
      const delta = Math.abs(Date.parse(point.at) - time)
      if (delta < distance) {
        closest = point
        distance = delta
      }
    }
    const value = closest?.[props.metric.key]
    if (distance <= props.history.stepSeconds * 1250 && value != null) {
      rows.push({
        id: server.id,
        name: server.name,
        color: colorFor(server),
        at: closest.at,
        value: props.metric.unit ? formatPercent(value) : formatLoad(value),
      })
    }
  }
  tooltip.value = rows.length ? {
    x,
    alignRight: x > plot.width / 2,
    rows,
  } : null
}

onMounted(() => {
  observer = new ResizeObserver(scheduleDraw)
  observer.observe(panel.value)
  scheduleDraw()
})
onBeforeUnmount(() => {
  observer?.disconnect()
  cancelAnimationFrame(frame)
})
watch(() => [props.history, props.metric, props.servers, props.range], async () => {
  tooltip.value = null
  await nextTick()
  scheduleDraw()
})
</script>

<template>
  <div ref="panel" class="chart-wrap">
    <canvas v-if="count" ref="chart" class="trend-canvas" role="img"
      :aria-label="`${metric.label}随时间变化的趋势图，采样数值可在下方的采样记录中查看`"
      @pointermove="showTooltip" @pointerleave="tooltip = null"></canvas>
    <div v-else class="chart-empty">
      <Icon name="trend" :size="32" />
      <span>该时段暂无{{ metric.label }}数据</span>
    </div>
    <template v-if="tooltip">
      <span class="chart-cursor" aria-hidden="true" :style="{ left: tooltip.x + 'px' }"></span>
      <div class="chart-tooltip" aria-hidden="true"
        :style="tooltip.alignRight ? { right: 'calc(100% - ' + tooltip.x + 'px + 10px)' } : { left: (tooltip.x + 10) + 'px' }">
        <div v-for="row in tooltip.rows" :key="row.id" class="grid grid-cols-[auto_1fr_auto] items-center gap-x-2 gap-y-1">
          <span class="legend-dot" :style="{ background: row.color }"></span>
          <span class="break-words">{{ row.name }}</span>
          <strong class="font-mono">{{ row.value }}</strong>
          <span class="col-start-2 text-muted">{{ formatTime(row.at) }}</span>
        </div>
      </div>
    </template>
  </div>
  <ul v-if="servers.length" class="flex flex-wrap gap-x-5 gap-y-2 px-1 text-sm text-muted" aria-label="图例">
    <li v-for="server in servers" :key="server.id" class="flex items-center gap-2">
      <span class="legend-dot" :style="{ background: colorFor(server) }"></span>{{ server.name }}
    </li>
  </ul>
</template>
