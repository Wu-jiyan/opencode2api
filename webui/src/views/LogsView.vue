<script setup>
import { computed, onMounted, onUnmounted, ref, shallowRef } from "vue";
import { NButton, NCard, NInput, NSelect, useMessage } from "naive-ui";

const message = useMessage();

const maxEvents = 2000;
const maxRendered = 500;

const events = shallowRef([]);
const paused = ref(false);
const level = ref("");
const component = ref("");
const keyword = ref("");
const autoScroll = ref(true);

let source = null;
let pending = false;
const listEl = ref(null);

const levelOptions = [
  { label: "全部级别", value: "" },
  { label: "debug", value: "debug" },
  { label: "info", value: "info" },
  { label: "warn", value: "warn" },
  { label: "error", value: "error" },
];

const visible = computed(() => {
  const selectedLevel = level.value;
  const selectedComponent = component.value.trim().toLowerCase();
  const search = keyword.value.trim().toLowerCase();
  return events.value
    .filter(
      (item) =>
        (!selectedLevel || item.level === selectedLevel) &&
        (!selectedComponent || (item.component || "").toLowerCase().includes(selectedComponent)) &&
        (!search || JSON.stringify(item).toLowerCase().includes(search)),
    )
    .slice(-maxRendered);
});

function scheduleRender() {
  if (paused.value || pending) return;
  pending = true;
  requestAnimationFrame(() => {
    pending = false;
    if (autoScroll.value && listEl.value) {
      listEl.value.scrollTop = listEl.value.scrollHeight;
    }
  });
}

function append(event) {
  const next = events.value.concat(event);
  events.value = next.length > maxEvents ? next.slice(next.length - maxEvents) : next;
  scheduleRender();
}

function connect() {
  if (source) source.close();
  source = new EventSource("/api/logs/stream");
  source.addEventListener("log", (event) => {
    try {
      append(JSON.parse(event.data));
    } catch {
      // 忽略无法解析的事件，保持流连接。
    }
  });
  source.addEventListener("gap", () => message.warning("较早的日志已从内存中清除"));
}

function togglePause() {
  paused.value = !paused.value;
  if (!paused.value && autoScroll.value && listEl.value) {
    listEl.value.scrollTop = listEl.value.scrollHeight;
  }
}

function onScroll() {
  const el = listEl.value;
  if (!el) return;
  autoScroll.value = el.scrollHeight - el.scrollTop - el.clientHeight < 40;
}

function levelClass(value) {
  return `log-level-${value || "debug"}`;
}

onMounted(connect);

onUnmounted(() => {
  if (source) source.close();
  source = null;
});
</script>

<template>
  <div>
    <div class="log-toolbar">
      <n-select v-model:value="level" :options="levelOptions" size="small" style="width: 140px" />
      <n-input
        v-model:value="component"
        placeholder="模块"
        clearable
        size="small"
        style="width: 150px"
      />
      <n-input
        v-model:value="keyword"
        placeholder="搜索事件、Request ID、模型"
        clearable
        size="small"
        style="width: 260px"
      />
      <n-button size="small" @click="togglePause">{{ paused ? "继续" : "暂停" }}</n-button>
      <span class="section-caption"
        >缓冲 {{ events.length }} 条，最多渲染 {{ maxRendered }} 条</span
      >
    </div>
    <div ref="listEl" class="log-list" @scroll="onScroll">
      <div v-for="(item, index) in visible" :key="item.sequence ?? index" class="log-row">
        <span class="log-time">{{ new Date(item.time).toLocaleTimeString() }}</span>
        <span :class="levelClass(item.level)">{{ item.level }}</span>
        <span class="log-component">{{ item.component || "core" }}</span>
        <span>{{ item.message }}</span>
        <span class="log-fields">{{ JSON.stringify(item.fields || {}) }}</span>
      </div>
      <div v-if="!visible.length" class="log-empty">等待日志事件…</div>
    </div>
  </div>
</template>
