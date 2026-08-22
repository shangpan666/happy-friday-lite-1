<template>
  <main class="login-page">
    <section class="login-panel">
      <h1>Happy Friday 企业版</h1>
      <p class="hint">请连接企业服务并登录后使用客户端。</p>
      <label>服务器地址</label>
      <input v-model="serverUrl" placeholder="例如 http://192.168.1.50:17918" autocomplete="url">
      <label>账号</label>
      <input v-model="username" placeholder="账号" autocomplete="username">
      <label>密码</label>
      <input v-model="password" type="password" placeholder="密码" autocomplete="current-password" @keyup.enter="login">
      <button :disabled="loading" @click="login">{{ loading ? '登录中...' : '登录' }}</button>
      <p v-if="error" class="error">{{ error }}</p>
    </section>
  </main>
</template>
<script setup>
import { ref } from 'vue'
import { enterpriseService } from '@/services/enterprise'
const emit = defineEmits(['authenticated'])
const serverUrl = ref(enterpriseService.serverUrl)
const username = ref('')
const password = ref('')
const loading = ref(false)
const error = ref('')
async function login() {
  error.value = ''
  if (!serverUrl.value || !username.value || !password.value) { error.value = '请填写服务器地址、账号和密码'; return }
  loading.value = true
  try { await enterpriseService.login(serverUrl.value, username.value, password.value); emit('authenticated') }
  catch (e) { error.value = e.message }
  finally { loading.value = false }
}
</script>
<style scoped>
.login-page{height:100vh;width:100vw;display:flex;align-items:center;justify-content:center;background:#f5f7fa}.login-panel{width:min(400px,calc(100vw - 40px));padding:32px;background:#fff;border:1px solid #e4e7ec;border-radius:10px;box-shadow:0 8px 30px rgba(16,24,40,.08)}h1{margin:0 0 8px;font-size:24px}.hint{color:#667085;margin:0 0 24px}label{display:block;margin:14px 0 6px;font-weight:600}input{box-sizing:border-box;width:100%;padding:11px;border:1px solid #d0d5dd;border-radius:6px;font-size:14px}button{width:100%;margin-top:20px;padding:11px;border:0;border-radius:6px;background:#1677ff;color:#fff;font-size:15px;cursor:pointer}button:disabled{opacity:.6;cursor:wait}.error{color:#b42318;margin:14px 0 0}
</style>
