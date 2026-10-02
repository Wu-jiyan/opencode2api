<script setup>
import { h, onMounted, onUnmounted, reactive, ref, watch } from "vue";
import { NAlert, NButton, NCard, NDataTable, NTag, useMessage } from "naive-ui";
import { api, post } from "../api";

const props = defineProps({ reloadSignal: { type: Number, default: 0 } });
const message = useMessage();

const intervalMs = 3000;
const loading = ref(false);
const snapshot = ref(null);
const probing = ref(false);
// Latency results keyed by proxy address so a reordered pool keeps its own.
const probes = ref({});
let timer = null;

const cards = reactive([
  { key: "total", label: "最近一小时请求", note: "滚动 60 分钟窗口", value: "—" },
  { key: "rate", label: "成功率", note: "HTTP 2xx–3xx", value: "—" },
  { key: "p95", label: "P95 延迟", note: "端到端毫秒", value: "—" },
  { key: "active", label: "当前并发", note: "请求 / 流式", value: "—" },
]);

const kindLabels = { vless: "VLESS", direct: "直连", static: "静态" };

function latencyTag(probe) {
  if (probe.ok) {
    const type = probe.millis < 300 ? "success" : probe.millis < 900 ? "warning" : "error";
    return h(
      NTag,
      { type, size: "small", bordered: false },
      { default: () => `${probe.millis} ms` },
    );
  }
  return h(
    NTag,
    { type: "error", size: "small", bordered: false, title: probe.error || "" },
    { default: () => "失败" },
  );
}

const proxyColumns = [
  {
    title: "节点",
    key: "node",
    minWidth: 200,
    ellipsis: { tooltip: true },
    render: (row) =>
      h("div", { class: "proxy-name", title: row.address }, [
        h(
          NTag,
          { type: row.kind === "vless" ? "info" : "default", size: "small", bordered: false },
          { default: () => kindLabels[row.kind] || row.kind },
        ),
        h(
          "span",
          { class: "mono proxy-label" },
          row.kind === "vless" ? row.node || "未就绪" : row.address,
        ),
      ]),
  },
  {
    title: "入口 / 传输",
    key: "upstream",
    minWidth: 200,
    ellipsis: { tooltip: true },
    render: (row) => [row.server, row.transport].filter(Boolean).join(" · ") || "—",
  },
  {
    title: "健康",
    key: "healthy",
    width: 96,
    render: (row) =>
      h(
        NTag,
        { type: row.healthy ? "success" : "error", size: "small", bordered: false },
        {
          default: () => (row.healthy ? "正常" : "不可用"),
        },
      ),
  },
  {
    title: "延迟",
    key: "latency",
    width: 104,
    render: (row) => {
      const probe = probes.value[row.address];
      if (!probe) return h("span", { class: "section-caption" }, "未测试");
      return latencyTag(probe);
    },
  },
  {
    title: "绑定",
    key: "binding",
    minWidth: 140,
    render: (row) => `Z ${row.zen_keys} · G ${row.go_keys} · A ${row.anonymous ? "开" : "关"}`,
  },
];

const keyColumns = [
  { title: "Key 尾码", key: "id", minWidth: 150 },
  { title: "Tier", key: "tier", width: 90 },
  {
    title: "状态",
    key: "state",
    width: 110,
    render: (row) =>
      h(
        NTag,
        { type: row.cooldown_until ? "warning" : "success", size: "small", bordered: false },
        { default: () => (row.cooldown_until ? "冷却中" : "可用") },
      ),
  },
];

function apply(data) {
  snapshot.value = data;
  const metrics = data.metrics || {};
  const lastHour = metrics.last_hour || {};
  cards[0].value = Number(lastHour.total || 0).toLocaleString();
  cards[1].value = `${((lastHour.success_rate || 0) * 100).toFixed(1)}%`;
  cards[2].value = `${lastHour.p95_ms || 0} ms`;
  cards[3].value = `${metrics.active_requests || 0} / ${metrics.active_streams || 0}`;
}

async function refresh(notify = false) {
  loading.value = true;
  try {
    apply(await api("/api/monitor"));
    if (notify) message.success("状态已刷新");
  } catch (error) {
    if (notify) message.error(error.message);
  } finally {
    loading.value = false;
  }
}

async function probe() {
  probing.value = true;
  try {
    const data = await post("/api/nodes/probe", {});
    const results = data.probes || [];
    probes.value = Object.fromEntries(results.map((item) => [item.address, item]));
    const failed = results.filter((item) => !item.ok).length;
    if (failed > 0) message.warning(`探测完成，${failed} / ${results.length} 个节点不可用`);
    else message.success(`探测完成，${results.length} 个节点均可用`);
  } catch (error) {
    message.error(error.message);
  } finally {
    probing.value = false;
  }
}

function startPolling() {
  stopPolling();
  timer = setInterval(() => refresh(false), intervalMs);
}

function stopPolling() {
  if (timer) clearInterval(timer);
  timer = null;
}

watch(
  () => props.reloadSignal,
  () => {
    probes.value = {};
    refresh(true);
  },
);

onMounted(() => {
  refresh(false);
  startPolling();
});

onUnmounted(stopPolling);
</script>

<template>
  <div>
    <div class="metric-grid">
      <n-card v-for="card in cards" :key="card.key" size="small" :bordered="false">
        <div class="section-caption">{{ card.label }}</div>
        <div class="mono" style="font-size: 26px; font-weight: 600; margin: 6px 0 2px">
          {{ card.value }}
        </div>
        <div class="section-caption">{{ card.note }}</div>
      </n-card>
    </div>

    <n-alert
      v-if="snapshot && snapshot.resources && snapshot.resources.anonymous"
      type="info"
      :bordered="false"
      class="block-gap"
    >
      匿名通道已启用：Zen 匿名请求会耗尽可用代理节点。
    </n-alert>

    <n-card title="代理节点" size="small" :bordered="false" class="block-gap">
      <template #header-extra>
        <div class="card-toolbar">
          <span class="section-caption">延迟为经该节点访问 Cloudflare 的首字节耗时</span>
          <n-button size="small" :loading="probing" @click="probe">测试延迟</n-button>
        </div>
      </template>
      <n-data-table
        :columns="proxyColumns"
        :data="snapshot?.resources?.proxies || []"
        :loading="loading && !snapshot"
        :bordered="false"
        size="small"
        :row-key="(row) => row.index"
        :max-height="360"
        virtual-scroll
      />
    </n-card>

    <n-card title="Key 池" size="small" :bordered="false">
      <template #header-extra>
        <span class="section-caption">仅显示 Key 尾码与冷却状态</span>
      </template>
      <n-data-table
        :columns="keyColumns"
        :data="snapshot?.resources?.keys || []"
        :loading="loading && !snapshot"
        :bordered="false"
        size="small"
        :row-key="(row) => `${row.tier}-${row.index}`"
        :max-height="300"
        virtual-scroll
      />
    </n-card>
  </div>
</template>
