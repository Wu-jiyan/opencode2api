<script setup>
import { NInput, NSpace, NTag } from "naive-ui";

defineProps({
  // 现有条目：[{ id, display }]
  items: { type: Array, default: () => [] },
  placeholder: { type: String, default: "每行新增一个" },
  rows: { type: Number, default: 2 },
});

// kept 保留的现有条目 id；added 为待新增的多行文本。
const kept = defineModel("kept", { type: Array, default: () => [] });
const added = defineModel("added", { type: String, default: "" });
</script>

<template>
  <n-space vertical :size="8" style="width: 100%">
    <n-space v-if="items.length" :size="6" align="center">
      <n-tag
        v-for="item in items.filter((entry) => kept.includes(entry.id))"
        :key="item.id"
        closable
        :bordered="false"
        @close="kept = kept.filter((id) => id !== item.id)"
      >
        <span class="mono">{{ item.display }}</span>
      </n-tag>
    </n-space>
    <n-input
      v-model:value="added"
      type="textarea"
      :rows="rows"
      :placeholder="placeholder"
      spellcheck="false"
    />
  </n-space>
</template>
