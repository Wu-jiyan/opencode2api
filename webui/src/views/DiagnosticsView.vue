<script setup>
import { computed, h, onMounted, ref, watch } from "vue";
import {
  NButton,
  NCard,
  NDataTable,
  NDescriptions,
  NDescriptionsItem,
  NInput,
  NTag,
  useMessage,
} from "naive-ui";
import { api, post } from "../api";

const props = defineProps({ reloadSignal: { type: Number, default: 0 } });
const message = useMessage();

const loading = ref(false);
const refreshing = ref(false);
const payload = ref(null);
const query = ref("");

const metadata = computed(() => payload.value?.metadata || {});
const catalog = computed(() => payload.value?.catalog || {});

const facts = computed(() => [
  ["元数据就绪", metadata.value.ready ? "是" : "否"],
  ["价格模型数", Number(metadata.value.models || 0).toLocaleString()],
  ["更新时间", formatTime(metadata.value.updated_at)],
  ["是否过期", metadata.value.stale ? "是" : "否"],
  ["下次刷新", formatTime(metadata.value.next_refresh)],
  ["最近错误", metadata.value.last_error || "无"],
  ["上游模型", `Zen ${catalog.value.zen || 0} · Go ${catalog.value.go || 0}`],
  ["可暴露模型", Number(catalog.value.exposed || 0).toLocaleString()],
]);

function formatTime(value) {
  if (!value) return "—";
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "—" : date.toLocaleString();
}

const rows = computed(() => {
  const keyword = query.value.trim().toLowerCase();
  return (payload.value?.models || []).filter(
    (item) =>
      !keyword ||
      item.model.toLowerCase().includes(keyword) ||
      (item.alias || "").toLowerCase().includes(keyword),
  );
});

// 判断来源是后端机器可读的判定原因，这里翻成人话，避免长串英文撑破列宽。
const sourceLabels = {
  name_free: "名称含 free",
  name_and_metadata_free: "名称与价格均为免费",
  metadata_free: "价格为零",
  metadata_paid: "价格为付费",
  metadata_deprecated: "已弃用",
  metadata_cost_unknown: "价格未知",
  metadata_model_missing: "元数据未收录",
  metadata_pending: "元数据未就绪",
  name_fallback_metadata_pending: "名称推断（元数据未就绪）",
};

const columns = [
  {
    title: "模型",
    key: "model",
    minWidth: 230,
    render: (row) =>
      h("div", null, [
        h("div", { class: "mono" }, row.model),
        row.alias ? h("div", { class: "section-caption" }, `对外 ${row.alias}`) : null,
      ]),
  },
  {
    title: "原生协议",
    key: "protocol",
    width: 170,
    render: (row) => `${row.native_protocol || "—"} · ${row.protocol_source || "—"}`,
  },
  {
    title: "路由",
    key: "route",
    minWidth: 150,
    render: (row) => {
      if (row.route_error) {
        // 完整原因放进原生 tooltip，列内只留一个定宽标签。
        return h(
          NTag,
          { type: "error", size: "small", bordered: false, title: row.route_error },
          { default: () => "不可路由" },
        );
      }
      const plan = [...(row.anonymous ? ["anonymous"] : []), ...(row.key_tiers || [])];
      return plan.length ? plan.join(" → ") : "—";
    },
  },
  {
    title: "匿名资格",
    key: "anonymous",
    width: 100,
    render: (row) => {
      const allowed = row.anonymous_eligibility?.allowed;
      return h(
        NTag,
        { type: allowed ? "success" : "default", size: "small", bordered: false },
        {
          default: () => (allowed ? "允许" : "拒绝"),
        },
      );
    },
  },
  {
    title: "判断来源",
    key: "source",
    minWidth: 200,
    ellipsis: { tooltip: true },
    render: (row) => {
      const source = row.anonymous_eligibility?.source;
      if (!source) return "—";
      return sourceLabels[source] || source;
    },
  },
  {
    title: "成本 input / output",
    key: "cost",
    minWidth: 170,
    render: (row) => {
      const eligibility = row.anonymous_eligibility || {};
      if (eligibility.input_cost == null && eligibility.output_cost == null) return "未知";
      const input = eligibility.input_cost == null ? "?" : eligibility.input_cost;
      const output = eligibility.output_cost == null ? "?" : eligibility.output_cost;
      return `${input} / ${output}`;
    },
  },
];

async function refresh(notify = false) {
  loading.value = true;
  try {
    payload.value = await api("/api/debug/models");
    if (notify) message.success("诊断数据已刷新");
  } catch (error) {
    if (notify) message.error(error.message);
  } finally {
    loading.value = false;
  }
}

// 手动刷新会真的去上游重拉模型目录和 models.dev 价格元数据，而不是只重读本地缓存。
async function refreshUpstream() {
  refreshing.value = true;
  try {
    payload.value = await post("/api/models/refresh", {});
    message.success("已从上游重新拉取模型目录与价格元数据");
  } catch (error) {
    message.error(error.message);
  } finally {
    refreshing.value = false;
  }
}

watch(
  () => props.reloadSignal,
  () => refresh(true),
);

onMounted(() => refresh(false));
</script>

<template>
  <div>
    <n-card title="模型元数据" size="small" :bordered="false" class="block-gap">
      <template #header-extra>
        <span class="section-caption"> 零成本或名称含 free 任一条件满足即可进入匿名通道 </span>
      </template>
      <n-descriptions :column="4" label-placement="top" size="small">
        <n-descriptions-item v-for="[label, value] in facts" :key="label" :label="label">
          <span class="mono">{{ value }}</span>
        </n-descriptions-item>
      </n-descriptions>
    </n-card>

    <n-card size="small" :bordered="false">
      <template #header>
        <span>模型路由表</span>
      </template>
      <template #header-extra>
        <div class="card-toolbar">
          <n-input
            v-model:value="query"
            placeholder="筛选模型"
            clearable
            size="small"
            style="width: 220px"
          />
          <n-button size="small" :loading="refreshing" @click="refreshUpstream">刷新</n-button>
        </div>
      </template>
      <n-data-table
        :columns="columns"
        :data="rows"
        :loading="loading"
        :bordered="false"
        size="small"
        :row-key="(row) => row.model"
        :max-height="560"
        virtual-scroll
      />
    </n-card>
  </div>
</template>
