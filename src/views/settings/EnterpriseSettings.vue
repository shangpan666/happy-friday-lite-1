<template>
  <div class="enterprise-settings">
    <h2>企业服务</h2>
    <p class="hint">配置企业内网服务后，笔记和日程将同步到服务端；未配置时继续使用本机数据。</p>
    <label>服务器地址</label>
    <input v-model="serverUrl" placeholder="例如 http://192.168.1.50:17918" @blur="checkHealth">
    <div class="actions"><button class="secondary" @click="checkHealth">检查连接</button><span v-if="healthText" class="status">{{ healthText }}</span></div>
    <template v-if="!enterpriseService.enabled">
      <label>账号</label><input v-model="username" autocomplete="username" placeholder="管理员或员工账号">
      <label>密码</label><input v-model="password" type="password" autocomplete="current-password" placeholder="密码" @keyup.enter="login">
      <button @click="login">登录企业服务</button>
    </template>
    <template v-else>
      <p class="status">当前已连接：{{ enterpriseService.serverUrl }}</p>
      <button class="danger" @click="logout">退出企业服务</button>
    </template>
    <p v-if="error" class="error">{{ error }}</p>
  </div>
</template>
<script setup>
import { ref } from 'vue'
import { enterpriseService } from '@/services/enterprise'
const serverUrl = ref(enterpriseService.serverUrl)
const username = ref('')
const password = ref('')
const healthText = ref('')
const error = ref('')
async function checkHealth() { error.value = ''; healthText.value = ''; try { await enterpriseService.health(serverUrl.value); healthText.value = '连接正常' } catch (e) { error.value = e.message } }
async function login() { error.value = ''; try { await enterpriseService.login(serverUrl.value, username.value, password.value); healthText.value = '登录成功'; username.value = ''; password.value = ''; location.reload() } catch (e) { error.value = e.message } }
function logout() { enterpriseService.logout(); location.reload() }
</script>
<style scoped>
.enterprise-settings{max-width:620px;padding:32px}.enterprise-settings h2{margin:0 0 8px}.hint{color:#667085;line-height:1.6;margin-bottom:24px}label{display:block;margin:14px 0 6px;font-weight:600}input{width:100%;box-sizing:border-box;padding:10px;border:1px solid #d0d5dd;border-radius:6px;font-size:14px}.actions{display:flex;align-items:center;gap:10px;margin:12px 0}.actions button{margin:0}.enterprise-settings button{padding:10px 16px;border:0;border-radius:6px;background:#1677ff;color:#fff;cursor:pointer}.enterprise-settings button.secondary{background:#eef4ff;color:#175cd3}.enterprise-settings button.danger{background:#d92d20}.status{color:#027a48}.error{color:#b42318;margin-top:16px}
</style>
