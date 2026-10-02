<script setup>
import { computed, defineAsyncComponent, onMounted, ref } from "vue";
import {
  NButton,
  NLayout,
  NLayoutContent,
  NLayoutHeader,
  NLayoutSider,
  NSpace,
  useMessage,
} from "naive-ui";
import { api, post } from "../api";

const props = defineProps({ session: { type: Object, required: true } });
const emit = defineEmits(["logged-out"]);
const message = useMessage();

// 视图按需加载：首屏只下载概览，其余页面在切换时才拉取。
const views = {
  overview: defineAsyncComponent(() => import("../views/OverviewView.vue")),
  diagnostics: defineAsyncComponent(() => import("../views/DiagnosticsView.vue")),
  config: defineAsyncComponent(() => import("../views/ConfigView.vue")),
  logs: defineAsyncComponent(() => import("../views/LogsView.vue")),
};

const pages = {
  overview: ["概览", "本网关自身的运行状态：请求量、延迟、代理与 Key 池健康。"],
  diagnostics: ["路由诊断", "模型的原生协议、路由优先级与匿名资格判断依据。"],
  config: ["配置中心", "上游 Key、代理池、vless 订阅、协议路由与性能参数。"],
  logs: ["事件日志", "进程内结构化日志的实时流。"],
};

const active = ref("overview");
const reloadSignal = ref(0);
const version = ref("");

const menuOptions = [
  { label: "概览", key: "overview" },
  { label: "路由诊断", key: "diagnostics" },
  { label: "配置中心", key: "config" },
  { label: "事件日志", key: "logs" },
];

const currentView = computed(() => views[active.value]);
const pageTitle = computed(() => pages[active.value][0]);
const pageSub = computed(() => pages[active.value][1]);

async function logout() {
  try {
    await post("/api/auth/logout");
  } catch {
    // 会话可能已失效；无论结果如何都回到登录页。
  }
  emit("logged-out");
}

function selectPage(key) {
  active.value = key;
}

function reload() {
  reloadSignal.value += 1;
  message.success("已刷新");
}

onMounted(async () => {
  try {
    version.value = (await api("/api/monitor")).version || "";
  } catch {
    version.value = "";
  }
});
</script>

<template>
  <n-layout has-sider position="absolute">
    <n-layout-sider class="shell-sider" :width="208">
      <div class="shell-brand">
        <span class="brand-badge">O2</span>
        <strong>opencode2api</strong>
      </div>
      <nav class="shell-nav">
        <n-button
          v-for="item in menuOptions"
          :key="item.key"
          class="shell-nav-item"
          block
          quaternary
          :type="active === item.key ? 'primary' : 'default'"
          @click="selectPage(item.key)"
        >
          {{ item.label }}
        </n-button>
      </nav>
      <div class="shell-footer">
        <div class="mono">{{ version ? `version ${version}` : "version —" }}</div>
      </div>
    </n-layout-sider>
    <n-layout>
      <n-layout-header class="shell-header" bordered>
        <div>
          <h1>{{ pageTitle }}</h1>
          <div class="page-sub">{{ pageSub }}</div>
        </div>
        <n-space :size="8">
          <n-button quaternary @click="reload">刷新</n-button>
          <n-button quaternary @click="logout">退出</n-button>
        </n-space>
      </n-layout-header>
      <n-layout-content class="shell-content">
        <component :is="currentView" :reload-signal="reloadSignal" />
      </n-layout-content>
    </n-layout>
  </n-layout>
</template>
