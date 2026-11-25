<script setup>
import { onMounted, useTemplateRef, watch } from 'vue'

const props = defineProps({ name: String, size: { type: Number, default: 20 } })
const canvas = useTemplateRef('icon')

const paths = {
  monitor: 'M3 5h18v12H3z M8 21h8 M12 17v4 M5.5 11H8l2-3 3 6 2-3h3.5',
  overview: 'M3 3h7v7H3z M14 3h7v7h-7z M3 14h7v7H3z M14 14h7v7h-7z',
  history: 'M3 11a9 9 0 1 1 2 7 M3 5v6h6 M12 8v5l3 2',
  server: 'M3 4h18v6H3z M3 14h18v6H3z M6 7h.01 M6 17h.01 M10 7h7 M10 17h7',
  refresh: 'M20.5 9A8.5 8.5 0 0 0 6 5.8L3.5 8.5 M3.5 4.5v4h4 M3.5 15A8.5 8.5 0 0 0 18 18.2l2.5-2.7 M20.5 19.5v-4h-4',
  pulse: 'M2.5 12H7l2.6-6 4.4 12 2.6-6h4.9',
  traffic: 'M8 20V4 M4 8l4-4 4 4 M16 4v16 M12 16l4 4 4-4',
  alert: 'M12 3l10 17H2z M12 9v4 M12 16h.01',
  pin: 'M19 10c0 5-7 10-7 10S5 15 5 10a7 7 0 1 1 14 0z M14.25 9.5a2.25 2.25 0 1 1-4.5 0 2.25 2.25 0 0 1 4.5 0z',
  clock: 'M21 12a9 9 0 1 1-18 0 9 9 0 0 1 18 0z M12 7v5l3 2',
  calendar: 'M4 6h16v14H4z M8 3v5 M16 3v5 M4 10h16',
  chevron: 'M6 9l6 6 6-6',
  trend: 'M3 20h18 M3 4v16 M6 14l4-4 4 3 6-7',
  layers: 'M3 8l9-5 9 5-9 5z M3 12l9 5 9-5 M3 16l9 5 9-5',
}

function draw() {
  const element = canvas.value
  if (!element) return
  const scale = Math.max(2, window.devicePixelRatio)
  element.width = element.height = Math.round(props.size * scale)
  const ctx = element.getContext('2d')
  if (!ctx || !paths[props.name]) return
  ctx.scale(element.width / 24, element.height / 24)
  ctx.strokeStyle = getComputedStyle(element).color
  ctx.lineWidth = 1.65
  ctx.lineCap = 'round'
  ctx.lineJoin = 'round'
  ctx.stroke(new Path2D(paths[props.name]))
}

onMounted(draw)
watch(() => [props.name, props.size], draw)
</script>

<template>
  <canvas ref="icon" aria-hidden="true" class="block shrink-0" :style="{ width: size + 'px', height: size + 'px' }"></canvas>
</template>
