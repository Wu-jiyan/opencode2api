<script setup>
import { onMounted, ref } from "vue";
import { NConfigProvider, NDialogProvider, NMessageProvider, NSpin } from "naive-ui";
import { api, setCsrfToken, setUnauthorizedHandler } from "./api";
import LoginView from "./views/LoginView.vue";
import AppShell from "./components/AppShell.vue";

// antd 风格的中性主题：单一主色，不使用渐变。
const themeOverrides = {
  common: {
    primaryColor: "#1677ff",
    primaryColorHover: "#4096ff",
    primaryColorPressed: "#0958d9",
    primaryColorSuppl: "#4096ff",
    borderRadius: "6px",
    fontFamily:
      '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", Helvetica, Arial, sans-serif',
  },
};

const booting = ref(true);
const session = ref(null);

function clearSession() {
  setCsrfToken("");
  session.value = null;
}

function applySession(value) {
  setCsrfToken(value?.csrf_token);
  session.value = value;
}

async function boot() {
  try {
    applySession(await api("/api/auth/session"));
  } catch {
    clearSession();
  } finally {
    booting.value = false;
  }
}

onMounted(() => {
  setUnauthorizedHandler(clearSession);
  boot();
});
</script>

<template>
  <n-config-provider :theme-overrides="themeOverrides">
    <n-message-provider>
      <n-dialog-provider>
        <div v-if="booting" class="login-shell">
          <n-spin size="large" />
        </div>
        <LoginView v-else-if="!session" @authenticated="applySession" />
        <AppShell v-else :session="session" @logged-out="clearSession" />
      </n-dialog-provider>
    </n-message-provider>
  </n-config-provider>
</template>
