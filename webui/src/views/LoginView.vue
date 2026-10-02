<script setup>
import { ref } from "vue";
import { NAlert, NButton, NCard, NForm, NFormItem, NInput } from "naive-ui";
import { post } from "../api";

const emit = defineEmits(["authenticated"]);

const username = ref("");
const password = ref("");
const error = ref("");
const loading = ref(false);

async function submit() {
  if (loading.value) return;
  error.value = "";
  loading.value = true;
  try {
    const session = await post("/api/auth/login", {
      username: username.value,
      password: password.value,
    });
    password.value = "";
    emit("authenticated", session);
  } catch (failure) {
    error.value = failure.message;
  } finally {
    loading.value = false;
  }
}
</script>

<template>
  <div class="login-shell">
    <n-card class="login-card" :bordered="false">
      <div class="login-brand">
        <span class="brand-badge">O2</span>
        <strong>opencode2api</strong>
        <span>控制台</span>
      </div>
      <n-form @submit.prevent="submit">
        <n-form-item label="账号">
          <n-input
            v-model:value="username"
            placeholder="管理账号"
            autocomplete="username"
            :input-props="{ autocomplete: 'username' }"
          />
        </n-form-item>
        <n-form-item label="密码">
          <n-input
            v-model:value="password"
            type="password"
            show-password-on="click"
            placeholder="管理密码"
            :input-props="{ autocomplete: 'current-password' }"
            @keyup.enter="submit"
          />
        </n-form-item>
        <n-alert v-if="error" type="error" :bordered="false" style="margin-bottom: 16px">
          {{ error }}
        </n-alert>
        <n-button type="primary" block :loading="loading" attr-type="submit" @click="submit">
          登录
        </n-button>
      </n-form>
    </n-card>
  </div>
</template>
