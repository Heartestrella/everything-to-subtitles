<script setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ArrowDownToLine, ArrowLeft, ArrowRight, Captions, Check, ChevronRight, CircleAlert, Cloud, FileAudio, FileVideo, Languages, LoaderCircle, Moon, Plus, Save, Settings2, Sun, UploadCloud, WandSparkles } from '@lucide/vue'

const jobs = ref([])
const recentJobs = ref(JSON.parse(localStorage.getItem('subtitle-recent-jobs') || '[]'))
const activeId = ref('')
const active = ref(null)
const route = ref('/new')
const page = computed(() => route.value === '/settings' ? 'settings' : route.value === '/tasks' ? 'tasks' : route.value.startsWith('/tasks/') ? 'detail' : 'new')
const showCreate = computed(() => page.value === 'new')
const theme = ref(localStorage.getItem('subtitle-theme') === 'dark' ? 'dark' : 'light')
const health = ref(null)
const authState = ref('')
const authLink = ref('')
const authCode = ref('')
const authBusy = ref(false)
const authError = ref('')
const file = ref(null)
const subtitleFile = ref(null)
const dragging = ref(false)
const uploading = ref(false)
const saving = ref(false)
const dirty = ref(false)
const notice = ref('')
const error = ref('')
const segments = ref([])
const media = ref(null)
const options = ref({ engine: 'auto', model: 'medium', source_language: '', target_language: 'zho_Hans', compute_type: 'float16', batch_size: 8 })
const languages = [
  ['zho_Hans', '简体中文'], ['zho_Hant', '繁體中文'], ['eng_Latn', 'English'],
  ['jpn_Jpan', '日本語'], ['kor_Hang', '한국어'], ['fra_Latn', 'Français'],
  ['deu_Latn', 'Deutsch'], ['spa_Latn', 'Español'], ['rus_Cyrl', 'Русский'],
  ['por_Latn', 'Português'], ['ita_Latn', 'Italiano'], ['arb_Arab', 'العربية'],
  ['hin_Deva', 'हिन्दी'], ['ind_Latn', 'Bahasa Indonesia'], ['vie_Latn', 'Tiếng Việt'],
  ['tha_Thai', 'ไทย'], ['tur_Latn', 'Türkçe'], ['ukr_Cyrl', 'Українська']
]
const statusText = { queued: '等待处理', running: '处理中', complete: '已完成', failed: '处理失败' }
const stageText = { queued: '排队', preparing: '准备环境', transcribing: '语音识别', aligning: '时间轴校准', translating: '字幕翻译', complete: '完成', fallback: '切换执行端', failed: '失败' }
const stageOrder = ['preparing', 'transcribing', 'aligning', 'translating', 'complete']
const isVideo = computed(() => active.value && /\.(mp4|mkv|mov|webm)$/i.test(active.value.file_name))
const completeCount = computed(() => jobs.value.filter(job => job.status === 'complete').length)
const currentStage = computed(() => stageOrder.indexOf(active.value?.stage))
watch(theme, value => { document.documentElement.dataset.theme = value; localStorage.setItem('subtitle-theme', value) }, { immediate: true })
function toggleTheme() { theme.value = theme.value === 'dark' ? 'light' : 'dark' }
function navigate(path) {
  if (route.value === path) return
  if (dirty.value && !window.confirm('当前字幕尚未保存，确定离开吗？')) return
  dirty.value = false
  if (window.location.hash !== `#${path}`) window.location.hash = path
  else syncRoute()
}
async function syncRoute() {
  const path = decodeURI(window.location.hash.slice(1)) || '/new'
  if (!/^\/(new|tasks(?:\/[^/]+)?|settings)$/.test(path)) { window.location.hash = '/new'; return }
  if (route.value !== path && dirty.value) {
    if (!window.confirm('当前字幕尚未保存，确定离开吗？')) { window.location.hash = route.value; return }
    dirty.value = false
  }
  route.value = path
  notice.value = ''
  if (path.startsWith('/tasks/')) {
    const id = path.slice('/tasks/'.length)
    if (activeId.value !== id) {
      activeId.value = id
      dirty.value = false
      try {
        active.value = await api(`/api/jobs/${encodeURIComponent(id)}`)
        segments.value = structuredClone(active.value.result?.segments || [])
      } catch (err) { active.value = null; error.value = err.message }
    }
  } else {
    activeId.value = ''
    active.value = null
    dirty.value = false
  }
}

async function api(url, init) {
  const response = await fetch(url, init)
  if (!response.ok) {
    const body = await response.json().catch(() => ({}))
    throw new Error(body.error || `请求失败 (${response.status})`)
  }
  return response.json()
}
async function refresh() {
  try {
    jobs.value = await api('/api/jobs')
    if (jobs.value.length) {
      recentJobs.value = jobs.value.slice(0, 20)
      localStorage.setItem('subtitle-recent-jobs', JSON.stringify(recentJobs.value))
    }
    if (activeId.value) {
      const next = await api(`/api/jobs/${activeId.value}`)
      const changed = next.updated_at !== active.value?.updated_at
      active.value = next
      if (changed && !dirty.value) segments.value = structuredClone(next.result?.segments || [])
    }
    error.value = ''
  } catch (err) { error.value = err.message }
}
async function selectJob(id) {
  const job = jobs.value.find(item => item.id === id) || recentJobs.value.find(item => item.id === id)
  if (job && !jobs.value.some(item => item.id === id)) {
    jobs.value = [job, ...jobs.value]
  }
  navigate(`/tasks/${id}`)
}
function openCreate() {
  navigate('/new')
}
function setFile(value) {
  if (!value) return
  file.value = value
  error.value = ''
}
function onDrop(event) {
  dragging.value = false
  setFile(event.dataTransfer?.files?.[0])
}
async function createJob() {
  if (!file.value && options.value.mode !== 'subtitle') { error.value = '请先选择音频或视频文件'; return }
  if (options.value.mode === 'subtitle' && !file.value && !subtitleFile.value) { error.value = '请至少选择媒体文件或字幕文件'; return }
  uploading.value = true
  error.value = ''
  try {
    const form = new FormData()
    if (file.value) form.append('file', file.value)
    if (subtitleFile.value) form.append('subtitle', subtitleFile.value)
    form.append('options', JSON.stringify(options.value))
    const job = await api('/api/jobs', { method: 'POST', body: form })
    file.value = null
    await refresh()
    await selectJob(job.id)
  } catch (err) { error.value = err.message }
  finally { uploading.value = false }
}
async function connectColab() {
  authBusy.value = true
  authError.value = ''
  try {
    const response = await api('/api/colab/connect', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' })
    authState.value = response.status
    authLink.value = response.url || ''
    health.value = await api('/api/health')
  } catch (err) { authError.value = err.message }
  finally { authBusy.value = false }
}
async function finishColabAuth() {
  if (!authCode.value.trim()) return
  authBusy.value = true
  authError.value = ''
  try {
    const response = await api('/api/colab/code', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ code: authCode.value.trim() }) })
    authState.value = response.status
    authLink.value = ''
    authCode.value = ''
  } catch (err) { authError.value = err.message }
  finally { authBusy.value = false }
}
function edit() { dirty.value = true; notice.value = '' }
async function save() {
  if (!active.value || !dirty.value) return
  saving.value = true
  try {
    active.value = await api(`/api/jobs/${active.value.id}/segments`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ segments: segments.value })
    })
    dirty.value = false
    notice.value = '字幕已保存'
  } catch (err) { error.value = err.message }
  finally { saving.value = false }
}
async function retry() {
  if (!active.value || active.value.status !== 'failed') return
  try { active.value = await api(`/api/jobs/${active.value.id}/retry`, { method: 'POST' }); await refresh() }
  catch (err) { error.value = err.message }
}
function jump(seconds) {
  if (media.value) { media.value.currentTime = seconds; media.value.play().catch(() => {}) }
}
async function exportTrack(format, track) {
  if (!active.value) return
  if (dirty.value) { await save(); if (dirty.value) return }
  window.location.href = `/api/jobs/${active.value.id}/export?format=${format}&track=${track}`
}
function formatTime(seconds) {
  const value = Number(seconds) || 0
  const minutes = Math.floor(value / 60).toString().padStart(2, '0')
  const secs = Math.floor(value % 60).toString().padStart(2, '0')
  return `${minutes}:${secs}`
}
function shortDate(value) { return value ? new Date(value).toLocaleString('zh-CN', { month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit' }) : '' }
function onKeydown(event) {
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 's' && dirty.value) { event.preventDefault(); save() }
}
let timer
onMounted(async () => {
  window.addEventListener('keydown', onKeydown)
  window.addEventListener('hashchange', syncRoute)
  await refresh()
  health.value = await api('/api/health').catch(() => null)
  await syncRoute()
  timer = window.setInterval(refresh, 2500)
})
onBeforeUnmount(() => { window.clearInterval(timer); window.removeEventListener('keydown', onKeydown); window.removeEventListener('hashchange', syncRoute) })
watch(() => active.value?.id, () => { if (media.value) media.value.load() })
</script>

<template>
  <div class="app-shell">
    <aside class="sidebar" aria-label="工作区">
      <div class="brand"><span class="brand-mark" aria-hidden="true"><i></i><i></i><i></i><i></i></span><span>声轨<span class="brand-en">SUBTITLE STUDIO</span></span></div>
      <div class="side-label">工作区</div>
      <button class="side-link" :class="{ selected: page === 'tasks' || page === 'detail' }" @click="navigate('/tasks')"><Captions :size="18" /> 字幕任务 <span class="side-count">{{ jobs.length }}</span></button>
      <button class="side-link" :class="{ selected: showCreate }" @click="openCreate"><Plus :size="18" /> 新建任务</button>
      <button class="side-link" :class="{ selected: page === 'settings' }" @click="navigate('/settings')"><Settings2 :size="18" /> 设置</button>
      <div class="side-label recent-label">最近任务</div>
      <div class="recent-list">
        <button v-for="job in jobs.slice(0, 8)" :key="job.id" class="recent-item" :class="{ active: activeId === job.id && page === 'detail' }" @click="selectJob(job.id)">
          <span class="recent-dot" :class="job.status"></span>
          <span class="recent-name">{{ job.file_name }}</span>
        </button>
        <p v-if="!jobs.length" class="recent-empty">上传媒体后，任务会出现在这里。</p>
      </div>
      <div class="sidebar-bottom"><button class="theme-button" @click="toggleTheme" :aria-label="theme === 'dark' ? '切换到浅色模式' : '切换到暗色模式'"><Sun v-if="theme === 'dark'" :size="16" /><Moon v-else :size="16" />{{ theme === 'dark' ? '浅色模式' : '暗色模式' }}</button><p>本地字幕工作台</p></div>
    </aside>

    <div class="workspace">
      <header class="topbar"><div class="breadcrumb">工作区 <ChevronRight :size="14" /> <strong>{{ page === 'new' ? '新建任务' : page === 'tasks' ? '字幕任务' : page === 'settings' ? '设置' : active?.file_name || '任务详情' }}</strong></div><div class="topbar-right"><span>{{ completeCount }} 个任务已完成</span><button class="top-new" @click="openCreate"><Plus :size="16" /> 新建任务</button></div></header>
      <main>
        <div v-if="error" class="alert error-alert" role="alert"><CircleAlert :size="18" /> <span>{{ error }}</span><button @click="error = ''" aria-label="关闭错误">×</button></div>

        <template v-if="showCreate">
          <div class="page-heading"><div><h1>新建字幕任务</h1><p class="heading-desc">上传媒体，选择目标语言。识别、对轴和翻译将依次完成。</p></div><span class="heading-icon"><WandSparkles :size="24" /></span></div>
          <div class="create-grid">
            <section class="panel upload-panel"><div class="panel-heading"><h2>媒体文件</h2><span>音频或视频</span></div>
              <label class="dropzone" :class="{ dragging, chosen: file }" @dragover.prevent="dragging = true" @dragleave.prevent="dragging = false" @drop.prevent="onDrop">
                <input type="file" accept=".mp4,.mkv,.mov,.webm,.mp3,.wav,.m4a,.flac,.aac,.ogg,audio/*,video/*" @change="setFile($event.target.files[0])" />
                <span class="upload-symbol"><component :is="file && /\.(mp4|mkv|mov|webm)$/i.test(file.name) ? FileVideo : file ? FileAudio : UploadCloud" :size="28" :stroke-width="1.7" /></span>
                <strong>{{ file ? file.name : '点击选择或拖放文件到这里' }}</strong><span>{{ file ? `${(file.size / 1024 / 1024).toFixed(1)} MB · 点击更换文件` : '支持 MP4、MKV、MOV、WebM、MP3、WAV 等格式，最大 4 GB' }}</span>
              </label>
              <div class="setting-divider"></div>
              <div class="panel-heading setting-heading"><h2>任务设置</h2><span>可在提交前调整</span></div>
              <div class="form-grid"><label class="field"><span>源语言</span><select v-model="options.source_language"><option value="">自动识别</option><option value="en">英语</option><option value="zh">中文</option><option value="ja">日语</option><option value="ko">韩语</option><option value="fr">法语</option><option value="de">德语</option><option value="es">西班牙语</option></select></label>
                <label class="field"><span>翻译目标语言</span><select v-model="options.target_language"><option v-for="[code, name] in languages" :key="code" :value="code">{{ name }}</option></select></label>
                <label class="field"><span>处理模式</span><select v-model="options.mode"><option value="transcribe">自动识别、对轴与翻译</option><option value="subtitle">只处理字幕，不识别/对轴/翻译</option></select></label><label v-if="options.mode === 'subtitle'" class="field"><span>导入字幕</span><input type="file" accept=".srt,.vtt" @change="subtitleFile = $event.target.files[0]" /></label>
                <label class="field"><span>执行端</span><select v-model="options.engine"><option value="auto">自动 · Colab 优先</option><option value="colab">仅 Colab GPU</option><option value="local">仅本机 GPU</option></select></label>
                <label class="field"><span>语音识别模型</span><select v-model="options.model"><option value="small">Whisper small · 更快</option><option value="medium">Whisper medium · 平衡</option><option value="large-v3">Whisper large-v3 · 更准</option></select></label></div>
              <button class="primary-button create-button" :disabled="!file || uploading" @click="createJob"><LoaderCircle v-if="uploading" class="spin" :size="18" /><ArrowRight v-else :size="18" />{{ uploading ? '正在上传文件…' : '开始制作字幕' }}</button>
            </section>
            <aside class="process-panel"><div class="process-header"><span class="process-icon"><Languages :size="20" /></span><h2>从声音到字幕</h2></div><p>处理完成后，可逐条校对并导出。</p><ol class="process-list"><li><span>1</span><div><strong>识别语音</strong><small>WhisperX 提取原文与语音片段</small></div></li><li><span>2</span><div><strong>校准时间</strong><small>强制对轴细化字幕起止时间</small></div></li><li><span>3</span><div><strong>翻译与校对</strong><small>NLLB-200 翻译后进入双语编辑器</small></div></li></ol><div class="process-note"><Cloud :size="17" /><span>Colab CLI 由软件管理。连接与执行环境可在设置页查看。</span></div><button class="process-settings" @click="navigate('/settings')">前往设置 <ArrowRight :size="15" /></button></aside>
          </div>
          <section class="panel home-recent-panel" aria-label="最近工作">
            <div class="panel-heading"><h2>最近任务</h2><button class="text-button" @click="navigate('/tasks')">查看全部 <ChevronRight :size="14" /></button></div>
            <div v-if="!(jobs.length || recentJobs.length)" class="home-recent-empty">上传媒体后，项目会自动出现在这里。</div>
            <div v-else class="home-recent-list">
              <button v-for="job in (jobs.length ? jobs : recentJobs).slice(0, 5)" :key="job.id" class="home-recent-row" @click="selectJob(job.id)">
                <span class="recent-dot" :class="job.status"></span><strong>{{ job.file_name }}</strong><span class="status-pill" :class="job.status"><span class="pill-dot"></span>{{ statusText[job.status] || job.status }}</span><span class="task-date">{{ shortDate(job.created_at) }}</span><ChevronRight :size="15" />
              </button>
            </div>
          </section>
        </template>

        <template v-else-if="page === 'tasks'">
          <div class="page-heading"><div><h1>字幕任务</h1><p class="heading-desc">查看处理进度，打开任务校对字幕。</p></div><button class="primary-button" @click="openCreate"><Plus :size="17" /> 新建任务</button></div>
          <section class="panel task-list" aria-label="任务列表">
            <div class="task-list-head"><span>文件</span><span>状态</span><span>执行端</span><span>创建时间</span></div>
            <button v-for="job in jobs" :key="job.id" class="task-row" @click="selectJob(job.id)"><span class="task-file"><component :is="/\.(mp4|mkv|mov|webm)$/i.test(job.file_name) ? FileVideo : FileAudio" :size="19" /><strong>{{ job.file_name }}</strong></span><span class="status-pill" :class="job.status"><span class="pill-dot"></span>{{ statusText[job.status] || job.status }}</span><span class="task-secondary">{{ job.engine === 'colab' ? 'Colab GPU' : job.engine === 'local' ? '本机 GPU' : '待分配' }}</span><span class="task-date">{{ shortDate(job.created_at) }}</span><ChevronRight class="task-arrow" :size="16" /></button>
            <div v-if="!jobs.length" class="task-empty"><Captions :size="30" /><h2>还没有字幕任务</h2><p>上传一段音频或视频，开始识别和翻译。</p><button class="primary-button" @click="openCreate"><Plus :size="16" /> 新建任务</button></div>
          </section>
        </template>

        <template v-else-if="page === 'settings'">
          <div class="page-heading"><div><h1>设置</h1><p class="heading-desc">管理界面外观、Colab 连接和本机执行环境。</p></div><span class="heading-icon"><Settings2 :size="23" /></span></div>
          <div class="settings-layout"><section class="panel settings-section"><h2>界面外观</h2><p>选择适合当前剪辑环境的显示模式。</p><div class="theme-options"><button :class="{ selected: theme === 'light' }" @click="theme = 'light'"><Sun :size="18" /> 浅色 <Check v-if="theme === 'light'" :size="16" /></button><button :class="{ selected: theme === 'dark' }" @click="theme = 'dark'"><Moon :size="18" /> 暗色 <Check v-if="theme === 'dark'" :size="16" /></button></div></section>
            <section class="panel settings-section"><h2>Colab 连接</h2><p>软件会在首次使用时安装官方 CLI。登录需要在浏览器内完成；暂时不连接也可以使用界面。</p><button class="outline-button" :disabled="authBusy" @click="connectColab"><Cloud :size="16" />{{ authBusy ? '正在连接…' : authState === 'connected' ? '已连接 Colab' : '连接 Colab' }}</button><div v-if="authState === 'awaiting_code'" class="auth-steps"><a :href="authLink" target="_blank" rel="noopener noreferrer">在浏览器打开 Google 授权页 ↗</a><label>在此输入 Google 页面显示的授权码<input v-model="authCode" type="password" autocomplete="off" spellcheck="false" /></label><button class="outline-button" :disabled="authBusy || !authCode.trim()" @click="finishColabAuth">完成连接</button></div><p v-if="authError" class="auth-error" role="alert">{{ authError }}</p></section>
            <section class="panel settings-section"><h2>执行环境</h2><p>任务运行所需的工具状态。</p><div class="environment-row"><span>Colab CLI</span><strong><i :class="health?.colab_cli ? 'ok-dot' : 'pending-dot'"></i>{{ health?.colab_cli ? '已安装' : '首次自动安装' }}</strong></div><div class="environment-row"><span>本机 Python</span><strong><i :class="health?.python ? 'ok-dot' : 'off-dot'"></i>{{ health?.python ? '已安装' : '未安装' }}</strong></div><div class="environment-row"><span>FFmpeg</span><strong><i :class="health?.ffmpeg ? 'ok-dot' : 'off-dot'"></i>{{ health?.ffmpeg ? '已安装' : '未安装' }}</strong></div></section></div>
        </template>

        <template v-else-if="active">
          <div class="detail-heading"><div><button class="back-link" @click="navigate('/tasks')"><ArrowLeft :size="15" /> 返回任务列表</button><h1>{{ active.file_name }}</h1><div class="detail-meta"><span class="status-pill" :class="active.status"><span class="pill-dot"></span>{{ statusText[active.status] || active.status }}</span><span>{{ shortDate(active.created_at) }}</span><span>{{ active.engine === 'colab' ? 'Colab GPU' : active.engine === 'local' ? '本机 GPU' : '等待分配' }}</span></div></div><button v-if="active.status === 'complete'" class="outline-button" :disabled="!dirty || saving" @click="save"><Save :size="16" />{{ saving ? '保存中…' : dirty ? '保存修改' : '已保存' }}</button></div>
          <template v-if="active.status !== 'complete'"><section class="panel progress-panel"><div class="progress-top"><div class="progress-symbol" :class="active.status"><CircleAlert v-if="active.status === 'failed'" :size="24" /><LoaderCircle v-else class="spin" :size="24" /></div><div><h2>{{ active.status === 'failed' ? '任务处理失败' : stageText[active.stage] || '处理中' }}</h2><p>{{ active.status === 'failed' ? active.error : active.detail }}</p></div></div><div class="stepper"><div v-for="(stage, index) in stageOrder.slice(0, 4)" :key="stage" class="step" :class="{ done: currentStage > index, current: currentStage === index }"><span><Check v-if="currentStage > index" :size="14" />{{ currentStage > index ? '' : index + 1 }}</span><small>{{ stageText[stage] }}</small></div></div><p class="progress-foot">处理可能需要下载模型和上传媒体；任务会在此页面持续更新。</p><button v-if="active.status === 'failed'" class="outline-button retry-button" @click="retry">重试任务 <ArrowRight :size="15" /></button></section></template>
          <template v-else>
            <div v-if="notice" class="alert success-alert" role="status"><Check :size="16" />{{ notice }}</div>
            <div class="editor-grid"><section class="media-panel"><div class="media-top"><span>媒体预览</span><span>{{ active.result?.segments?.length || 0 }} 条字幕</span></div><video v-if="isVideo" ref="media" controls preload="metadata" :src="`/api/jobs/${active.id}/media`"></video><div v-else class="audio-art"><div class="audio-lines"><i v-for="n in 28" :key="n" :style="{ height: `${14 + ((n * 17) % 58)}px` }"></i></div><FileAudio :size="28" /></div><audio v-if="!isVideo" ref="media" controls preload="metadata" :src="`/api/jobs/${active.id}/media`"></audio><div class="media-caption"><span>识别语言</span><strong>{{ active.result?.language || '未知' }}</strong><span>目标语言</span><strong>{{ languages.find(item => item[0] === active.options.target_language)?.[1] || active.options.target_language }}</strong><span>时间轴</span><strong>{{ active.result?.alignment === 'forced' ? '强制对轴' : active.result?.alignment === 'transcription' ? '识别时间轴' : '未知' }}</strong></div><p v-if="active.result?.alignment_note" class="alignment-note">{{ active.result.alignment_note }}</p><div class="export-box"><h3>导出字幕</h3><p>选择轨道与格式，保留当前校对结果。</p><div class="export-actions"><button @click="exportTrack('srt', 'bilingual')"><ArrowDownToLine :size="15" /> 双语 SRT</button><button @click="exportTrack('srt', 'translation')"><ArrowDownToLine :size="15" /> 译文 SRT</button><button @click="exportTrack('srt', 'original')"><ArrowDownToLine :size="15" /> 原文 SRT</button><button @click="exportTrack('vtt', 'bilingual')"><ArrowDownToLine :size="15" /> 双语 VTT</button></div></div></section>
            <section class="transcript-panel"><div class="transcript-heading"><div><h2>字幕校对</h2><p>点击时间点定位播放，修改内容后保存。</p></div><span>{{ segments.length }} 条</span></div><div v-if="!segments.length" class="no-segments">没有检测到可用语音片段。</div><div v-else class="segments"><article v-for="(segment, index) in segments" :key="index" class="segment"><div class="segment-index">{{ String(index + 1).padStart(2, '0') }}</div><div class="segment-body"><div class="segment-time"><button @click="jump(segment.start)" :aria-label="`跳转到第 ${index + 1} 条字幕`">{{ formatTime(segment.start) }}</button><label><span class="sr-only">开始时间，秒</span><input type="number" min="0" step="0.01" v-model.number="segment.start" @input="edit" /></label><span>—</span><label><span class="sr-only">结束时间，秒</span><input type="number" min="0" step="0.01" v-model.number="segment.end" @input="edit" /></label></div><label class="segment-label">原文<textarea rows="2" v-model="segment.text" @input="edit"></textarea></label><label class="segment-label translated">译文<textarea rows="2" v-model="segment.translation" @input="edit"></textarea></label></div></article></div></section></div>
          </template>
        </template>
      </main>
    </div>
  </div>
</template>


