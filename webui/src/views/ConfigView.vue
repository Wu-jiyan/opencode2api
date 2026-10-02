<script setup>
import { onMounted, reactive, ref } from "vue";
import {
  NAlert,
  NButton,
  NCard,
  NForm,
  NFormItem,
  NGrid,
  NGi,
  NInput,
  NInputNumber,
  NSelect,
  NSpace,
  NSwitch,
  useMessage,
} from "naive-ui";
import { api, post, put } from "../api";
import SecretTags from "../components/SecretTags.vue";

const message = useMessage();

const loading = ref(false);
const saving = ref(false);
const restartFields = ref([]);
const effective = ref(null);

const form = reactive({
  listen: "",
  prefer: "go",
  webListen: "",
  webEnabled: true,
  sessionTtl: 720,
  anonymous: false,
  proxyfile: "",
  upZen: "",
  upGo: "",
  protocols: "{}",
  reasoningEffort: "",
  reasoningByModel: "{}",
  retry: { maxAttempts: 3, timeoutSeconds: 300 },
  models: { refreshSeconds: 300, stripFreeSuffix: false },
  performance: {
    maxIdleConns: 2048,
    maxIdleConnsPerHost: 256,
    maxConnsPerHost: 0,
    idleConnTimeoutSeconds: 120,
    connectTimeoutSeconds: 5,
    failureCooldownSeconds: 15,
    attemptTimeoutSeconds: 0,
    connectionRotationSeconds: 0,
  },
  logging: { level: "info", ringSize: 2000, dumpRequestBodies: false },
  vless: {
    enabled: false,
    subscription: "",
    count: 24,
    refreshSeconds: 300,
    rotateBatch: 3,
    xrayPath: "",
    portBase: 24000,
    host: "127.0.0.1",
    subscriptionProxy: "",
    startupTimeoutSeconds: 15,
  },
});

const secretNames = ["server_keys", "zen_keys", "go_keys", "proxies"];
// 首次渲染发生在 onMounted 拉取配置之前，因此这里先给出完整形状。
const secrets = reactive(
  Object.fromEntries(secretNames.map((name) => [name, { items: [], kept: [], added: "" }])),
);

const preferOptions = [
  { label: "Go（失败回退 Zen）", value: "go" },
  { label: "Zen（失败回退 Go）", value: "zen" },
];

const effortOptions = [
  { label: "跟随客户端", value: "" },
  { label: "minimal", value: "minimal" },
  { label: "low", value: "low" },
  { label: "medium", value: "medium" },
  { label: "high", value: "high" },
  { label: "xhigh", value: "xhigh" },
  { label: "max", value: "max" },
  { label: "关闭", value: "none" },
];

const levelOptions = ["debug", "info", "warn", "error"].map((value) => ({ label: value, value }));

function fill(value) {
  form.listen = value.listen;
  form.prefer = value.prefer;
  form.webListen = value.webui.listen;
  form.webEnabled = value.webui.enabled;
  form.sessionTtl = value.webui.session_ttl_minutes;
  form.anonymous = !!value.anonymous;
  form.proxyfile = value.proxyfile || "";
  form.upZen = value.upstream.zen;
  form.upGo = value.upstream.go;
  form.protocols = JSON.stringify(value.models.protocols || {}, null, 2);
  form.reasoningEffort = value.reasoning?.effort || "";
  form.reasoningByModel = JSON.stringify(value.reasoning?.effort_by_model || {}, null, 2);
  form.retry.maxAttempts = value.retry.max_attempts;
  form.retry.timeoutSeconds = value.retry.timeout_seconds;
  form.models.refreshSeconds = value.models.refresh_seconds;
  form.models.stripFreeSuffix = !!value.models.strip_free_suffix;
  const performance = value.performance || {};
  form.performance.maxIdleConns = performance.max_idle_conns;
  form.performance.maxIdleConnsPerHost = performance.max_idle_conns_per_host;
  form.performance.maxConnsPerHost = performance.max_conns_per_host;
  form.performance.idleConnTimeoutSeconds = performance.idle_conn_timeout_seconds;
  form.performance.connectTimeoutSeconds = performance.connect_timeout_seconds;
  form.performance.failureCooldownSeconds = performance.failure_cooldown_seconds;
  form.performance.attemptTimeoutSeconds = performance.attempt_timeout_seconds;
  form.performance.connectionRotationSeconds = performance.connection_rotation_seconds ?? 0;
  const logging = value.logging || {};
  form.logging.level = logging.level;
  form.logging.ringSize = logging.ring_size;
  form.logging.dumpRequestBodies = !!logging.dump_request_bodies;
  const vless = value.vless || {};
  Object.assign(form.vless, {
    enabled: !!vless.enabled,
    subscription: vless.subscription || "",
    count: vless.count ?? 24,
    refreshSeconds: vless.refresh_seconds ?? 300,
    rotateBatch: vless.rotate_batch ?? 3,
    xrayPath: vless.xray_path || "",
    portBase: vless.port_base ?? 24000,
    host: vless.host || "127.0.0.1",
    subscriptionProxy: vless.subscription_proxy || "",
    startupTimeoutSeconds: vless.startup_timeout_seconds ?? 15,
  });

  secretNames.forEach((name) => {
    const items = value[name] || [];
    secrets[name] = { items, kept: items.map((item) => item.id), added: "" };
  });

  restartFields.value = value.restart_required_fields || [];
  effective.value = value.effective || null;
}

function resolveSecret(name) {
  const entry = secrets[name];
  const kept = entry.kept.map((id) => ({ id }));
  const added = entry.added
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .map((value) => ({ value }));
  return kept.concat(added);
}

function parseJson(label, text) {
  try {
    return JSON.parse(text || "{}");
  } catch (error) {
    throw new Error(`${label} 不是合法 JSON：${error.message}`);
  }
}

async function load() {
  loading.value = true;
  try {
    fill(await api("/api/config"));
  } catch (error) {
    message.error(error.message);
  } finally {
    loading.value = false;
  }
}

async function save() {
  if (saving.value) return;
  saving.value = true;
  try {
    const update = {
      listen: form.listen,
      server_keys: resolveSecret("server_keys"),
      zen_keys: resolveSecret("zen_keys"),
      go_keys: resolveSecret("go_keys"),
      anonymous: form.anonymous,
      proxies: resolveSecret("proxies"),
      proxyfile: form.proxyfile,
      vless: {
        enabled: form.vless.enabled,
        subscription: form.vless.subscription,
        count: form.vless.count,
        refresh_seconds: form.vless.refreshSeconds,
        rotate_batch: form.vless.rotateBatch,
        xray_path: form.vless.xrayPath,
        port_base: form.vless.portBase,
        host: form.vless.host,
        subscription_proxy: form.vless.subscriptionProxy,
        startup_timeout_seconds: form.vless.startupTimeoutSeconds,
      },
      upstream: { zen: form.upZen, go: form.upGo },
      retry: {
        max_attempts: form.retry.maxAttempts,
        timeout_seconds: form.retry.timeoutSeconds,
      },
      models: {
        refresh_seconds: form.models.refreshSeconds,
        protocols: parseJson("模型协议覆盖", form.protocols),
        strip_free_suffix: form.models.stripFreeSuffix,
      },
      reasoning: {
        effort: form.reasoningEffort,
        effort_by_model: parseJson("按模型思考强度", form.reasoningByModel),
      },
      performance: {
        max_idle_conns: form.performance.maxIdleConns,
        max_idle_conns_per_host: form.performance.maxIdleConnsPerHost,
        max_conns_per_host: form.performance.maxConnsPerHost,
        idle_conn_timeout_seconds: form.performance.idleConnTimeoutSeconds,
        connect_timeout_seconds: form.performance.connectTimeoutSeconds,
        failure_cooldown_seconds: form.performance.failureCooldownSeconds,
        attempt_timeout_seconds: form.performance.attemptTimeoutSeconds,
        connection_rotation_seconds: form.performance.connectionRotationSeconds ?? 0,
      },
      logging: {
        level: form.logging.level,
        ring_size: form.logging.ringSize,
        dump_request_bodies: form.logging.dumpRequestBodies,
      },
      prefer: form.prefer,
      webui: {
        enabled: form.webEnabled,
        listen: form.webListen,
        session_ttl_minutes: form.sessionTtl,
      },
    };
    const result = await put("/api/config", update);
    fill(result.config);
    const restart = result.result?.restart_required;
    message.success(restart ? "已保存；监听字段需重启进程生效" : "配置已保存并热应用");
  } catch (error) {
    message.error(error.message);
  } finally {
    saving.value = false;
  }
}

async function reload() {
  try {
    const result = await post("/api/config/reload");
    await load();
    message.success(result.restart_required ? "已从磁盘重载；监听字段需重启" : "已从磁盘重载");
  } catch (error) {
    message.error(error.message);
  }
}

onMounted(load);
</script>

<template>
  <n-form :disabled="loading" label-placement="top" @submit.prevent="save">
    <n-alert v-if="restartFields.length" type="warning" :bordered="false" class="block-gap">
      已保存但尚未生效：{{ restartFields.join("、") }}。当前 API {{ effective?.listen }}、WebUI
      {{ effective?.webui_listen }}，请重启进程。
    </n-alert>

    <n-card title="服务与管理端" size="small" :bordered="false" class="block-gap">
      <template #header-extra>
        <span class="section-caption">监听地址变更保存后需重启</span>
      </template>
      <n-grid :cols="3" :x-gap="16">
        <n-gi>
          <n-form-item label="认证 Key 首选 Tier">
            <n-select v-model:value="form.prefer" :options="preferOptions" />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="API Listen">
            <n-input v-model:value="form.listen" />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="WebUI Listen">
            <n-input v-model:value="form.webListen" />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="Session TTL（分钟）">
            <n-input-number
              v-model:value="form.sessionTtl"
              :min="5"
              :max="10080"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi :span="2">
          <n-form-item label="启用 WebUI（修改需重启）">
            <n-switch v-model:value="form.webEnabled" />
          </n-form-item>
        </n-gi>
      </n-grid>
    </n-card>

    <n-card title="密钥与匿名通道" size="small" :bordered="false" class="block-gap">
      <template #header-extra>
        <span class="section-caption">现有值以掩码展示，移除后保存才生效</span>
      </template>
      <n-grid :cols="2" :x-gap="16">
        <n-gi :span="2">
          <n-form-item label="启用 Zen 匿名通道">
            <n-switch v-model:value="form.anonymous" />
          </n-form-item>
        </n-gi>
        <n-gi :span="2">
          <n-form-item label="Server Keys（客户端访问本网关）">
            <SecretTags
              v-model:kept="secrets.server_keys.kept"
              v-model:added="secrets.server_keys.added"
              :items="secrets.server_keys.items"
              placeholder="每行新增一个 key"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="Zen Keys">
            <SecretTags
              v-model:kept="secrets.zen_keys.kept"
              v-model:added="secrets.zen_keys.added"
              :items="secrets.zen_keys.items"
              placeholder="每行新增一个 key"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="Go Keys">
            <SecretTags
              v-model:kept="secrets.go_keys.kept"
              v-model:added="secrets.go_keys.added"
              :items="secrets.go_keys.items"
              placeholder="每行新增一个 key"
            />
          </n-form-item>
        </n-gi>
      </n-grid>
    </n-card>

    <n-card title="静态代理节点" size="small" :bordered="false" class="block-gap">
      <template #header-extra>
        <span class="section-caption">支持 direct、HTTP(S) 与 SOCKS URL</span>
      </template>
      <n-grid :cols="2" :x-gap="16">
        <n-gi :span="2">
          <n-form-item label="Proxies">
            <SecretTags
              v-model:kept="secrets.proxies.kept"
              v-model:added="secrets.proxies.added"
              :items="secrets.proxies.items"
              placeholder="direct&#10;http://user:pass@host:port"
              :rows="3"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="Proxy File">
            <n-input v-model:value="form.proxyfile" placeholder="proxies.txt" />
          </n-form-item>
        </n-gi>
      </n-grid>
    </n-card>

    <n-card title="vless 代理池" size="small" :bordered="false" class="block-gap">
      <template #header-extra>
        <span class="section-caption">订阅节点各由一个 Xray 进程承载，独立出口 IP</span>
      </template>
      <n-grid :cols="3" :x-gap="16">
        <n-gi>
          <n-form-item label="启用 vless 代理池">
            <n-switch v-model:value="form.vless.enabled" />
          </n-form-item>
        </n-gi>
        <n-gi :span="2">
          <n-form-item label="订阅地址（兼容 v2ray / Clash）">
            <n-input v-model:value="form.vless.subscription" placeholder="https://…" />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="节点数量">
            <n-input-number
              v-model:value="form.vless.count"
              :min="1"
              :max="256"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="刷新间隔（秒）">
            <n-input-number
              v-model:value="form.vless.refreshSeconds"
              :min="10"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="每轮重建数量">
            <n-input-number
              v-model:value="form.vless.rotateBatch"
              :min="1"
              :max="form.vless.count"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="监听地址">
            <n-input v-model:value="form.vless.host" />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="端口起点">
            <n-input-number v-model:value="form.vless.portBase" :min="1024" style="width: 100%" />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="启动超时（秒）">
            <n-input-number
              v-model:value="form.vless.startupTimeoutSeconds"
              :min="1"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi :span="2">
          <n-form-item label="Xray 路径（留空自动查找 bin/xray/xray）">
            <n-input v-model:value="form.vless.xrayPath" placeholder="bin/xray/xray" />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="拉取订阅所用代理（可选）">
            <n-input
              v-model:value="form.vless.subscriptionProxy"
              placeholder="socks5://127.0.0.1:1080"
            />
          </n-form-item>
        </n-gi>
      </n-grid>
    </n-card>

    <n-card title="上游与协议路由" size="small" :bordered="false" class="block-gap">
      <template #header-extra>
        <span class="section-caption">显式协议映射优先于模型名称推断</span>
      </template>
      <n-grid :cols="3" :x-gap="16">
        <n-gi>
          <n-form-item label="Zen URL">
            <n-input v-model:value="form.upZen" />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="Go URL">
            <n-input v-model:value="form.upGo" />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="模型刷新（秒）">
            <n-input-number
              v-model:value="form.models.refreshSeconds"
              :min="1"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item
            label="剥离模型名 -free 后缀"
            feedback="开启后 /v1/models 返回去掉 -free 的 ID，两种写法都可调用"
          >
            <n-switch v-model:value="form.models.stripFreeSuffix" />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="每 Tier 最大尝试次数">
            <n-input-number v-model:value="form.retry.maxAttempts" :min="1" style="width: 100%" />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="请求超时（秒）">
            <n-input-number
              v-model:value="form.retry.timeoutSeconds"
              :min="1"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi :span="3">
          <n-form-item label="模型协议覆盖（JSON）">
            <n-input v-model:value="form.protocols" type="textarea" :rows="4" spellcheck="false" />
          </n-form-item>
        </n-gi>
      </n-grid>
    </n-card>

    <n-card title="Thinking 强度" size="small" :bordered="false" class="block-gap">
      <template #header-extra>
        <span class="section-caption">仅在客户端未明确指定时生效；按模型覆盖默认值</span>
      </template>
      <n-grid :cols="3" :x-gap="16">
        <n-gi>
          <n-form-item label="默认强度">
            <n-select v-model:value="form.reasoningEffort" :options="effortOptions" />
          </n-form-item>
        </n-gi>
        <n-gi :span="2">
          <n-form-item label="按模型覆盖（JSON）">
            <n-input
              v-model:value="form.reasoningByModel"
              type="textarea"
              :rows="3"
              placeholder='{"model-id":"high"}'
              spellcheck="false"
            />
          </n-form-item>
        </n-gi>
      </n-grid>
    </n-card>

    <n-card title="性能与日志" size="small" :bordered="false" class="block-gap">
      <template #header-extra>
        <span class="section-caption">热重载会保留进程内监控累计</span>
      </template>
      <n-grid :cols="3" :x-gap="16">
        <n-gi>
          <n-form-item label="Max Idle Conns">
            <n-input-number
              v-model:value="form.performance.maxIdleConns"
              :min="1"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="Max Idle / Host">
            <n-input-number
              v-model:value="form.performance.maxIdleConnsPerHost"
              :min="1"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="Max Conns / Host（0 为不限）">
            <n-input-number
              v-model:value="form.performance.maxConnsPerHost"
              :min="0"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="Idle Timeout（秒）">
            <n-input-number
              v-model:value="form.performance.idleConnTimeoutSeconds"
              :min="1"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="Connect Timeout（秒）">
            <n-input-number
              v-model:value="form.performance.connectTimeoutSeconds"
              :min="1"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="Failure Cooldown（秒）">
            <n-input-number
              v-model:value="form.performance.failureCooldownSeconds"
              :min="1"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="Attempt Timeout（秒，0 为跟随请求超时）">
            <n-input-number
              v-model:value="form.performance.attemptTimeoutSeconds"
              :min="0"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="连接轮换（秒，0 为不轮换）">
            <n-input-number
              v-model:value="form.performance.connectionRotationSeconds"
              :min="0"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="日志级别">
            <n-select v-model:value="form.logging.level" :options="levelOptions" />
          </n-form-item>
        </n-gi>
        <n-gi>
          <n-form-item label="日志 Ring Size">
            <n-input-number
              v-model:value="form.logging.ringSize"
              :min="100"
              :max="50000"
              style="width: 100%"
            />
          </n-form-item>
        </n-gi>
        <n-gi :span="3">
          <n-form-item label="转储出站请求体（已脱敏，仅用于排查重试与转换问题）">
            <n-switch v-model:value="form.logging.dumpRequestBodies" />
          </n-form-item>
        </n-gi>
      </n-grid>
      <template #action>
        <n-space justify="end">
          <n-button quaternary :disabled="saving" @click="reload">从磁盘重载</n-button>
          <n-button type="primary" :loading="saving" attr-type="submit" @click="save">
            验证、保存并应用
          </n-button>
        </n-space>
      </template>
    </n-card>
  </n-form>
</template>
