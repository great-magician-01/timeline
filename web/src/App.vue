<script setup lang="ts">
import { onMounted, ref } from 'vue'

// 主题切换（docs/DESIGN_SYSTEM.md §8）：data-theme + localStorage，首帧由 index.html 内联脚本处理
const theme = ref<'light' | 'dark'>(
  (document.documentElement.getAttribute('data-theme') as 'light' | 'dark') || 'light',
)
function toggleTheme() {
  theme.value = theme.value === 'dark' ? 'light' : 'dark'
  document.documentElement.setAttribute('data-theme', theme.value)
  try {
    localStorage.setItem('tl-theme', theme.value)
  } catch {
    /* 忽略存储失败 */
  }
}

// 骨架期自检：展示后端健康与就绪状态
const health = ref('检测中…')
const ready = ref('检测中…')

async function check() {
  health.value = await probe('/healthz')
  ready.value = await probe('/readyz', true)
}

async function probe(url: string, showBody = false): Promise<string> {
  try {
    const resp = await fetch(url)
    if (!showBody) return resp.ok ? '正常' : `异常（${resp.status}）`
    const body = await resp.json().catch(() => ({}))
    const detail = `database=${body.database ?? '?'} migrations=${body.migrations ?? '?'}`
    return resp.ok ? `就绪（${detail}）` : `未就绪（${detail}）`
  } catch {
    return '无法连接'
  }
}

onMounted(check)
</script>

<template>
  <div class="shell">
    <header class="topbar">
      <a class="brand" href="/" aria-label="时间线首页">
        <svg class="mark" viewBox="0 0 64 64" aria-hidden="true">
          <rect width="64" height="64" rx="14" fill="#fff" />
          <path
            d="M9.5 12.5C17.5 10 46.5 10 53.5 12.8C55.8 20.5 55.8 43.5 53.8 50.8C46 53.5 18 53.5 10.8 50.5C8.6 43.5 8.6 20.5 9.5 12.5Z"
            fill="none"
            stroke="#111"
            stroke-width="4.5"
            stroke-linecap="round"
            stroke-linejoin="round"
          />
          <g
            transform="rotate(-2 32 32)"
            fill="none"
            stroke="#111"
            stroke-width="4"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <path
              d="M21.2 21C25.4 20.3 29.1 20.3 31.3 21C32.1 25.4 32.1 33.6 31.3 37.9C27.6 38.6 23.9 38.6 21.2 37.9C20.3 33.6 20.3 25.4 21.2 21Z"
            />
            <path d="M21.4 29.6C24.4 29 27.8 29 30.9 29.6" />
            <path d="M33.8 24.2C37.8 23.4 42.8 23.4 47.2 24.1" />
            <path d="M41.8 17C42.6 24.6 42.6 35.4 41.8 44.6C39.6 45.3 37.1 44.4 36.2 41.9" />
            <circle cx="36.4" cy="33.2" r="2.4" fill="#111" stroke="none" />
          </g>
        </svg>
        <span class="wordmark">时间线</span>
      </a>

      <button class="theme-toggle" type="button" aria-label="切换浅色/深色主题" @click="toggleTheme">
        <svg v-if="theme === 'light'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true">
          <path d="M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5z" />
        </svg>
        <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" aria-hidden="true">
          <circle cx="12" cy="12" r="4.2" />
          <path d="M12 2.5v2.4M12 19.1v2.4M2.5 12h2.4M19.1 12h2.4M5.2 5.2l1.7 1.7M17.1 17.1l1.7 1.7M18.8 5.2l-1.7 1.7M6.9 17.1l-1.7 1.7" />
        </svg>
      </button>
    </header>

    <main class="content">
      <section class="card">
        <h1>工程骨架已就绪</h1>
        <p>后端（Go）与前端（Vue）连通性自检：</p>
        <ul class="status">
          <li>存活检查 /healthz：<strong>{{ health }}</strong></li>
          <li>就绪检查 /readyz：<strong>{{ ready }}</strong></li>
        </ul>
        <button class="refresh" type="button" @click="check">重新检测</button>
        <p class="hint">
          下一步按实施顺序接入用户体系与内容模块；设计与约定见项目 <code>docs/</code> 目录。
        </p>
      </section>
    </main>
  </div>
</template>

<style scoped>
.shell {
  min-height: 100vh;
}
.topbar {
  position: sticky;
  top: 0;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 0 16px;
  height: 60px;
  background: var(--glass);
  backdrop-filter: blur(6px);
  border-bottom: 1px solid var(--border);
}
.brand {
  display: flex;
  align-items: center;
  gap: 10px;
  color: var(--ink);
}
.brand:hover {
  text-decoration: none;
}
.mark {
  width: 28px;
  height: 28px;
  display: block;
}
.wordmark {
  font-size: 17px;
  font-weight: 800;
  letter-spacing: 0.04em;
}
.theme-toggle {
  margin-left: auto;
  width: 34px;
  height: 34px;
  border-radius: 8px;
  display: flex;
  align-items: center;
  justify-content: center;
  color: var(--ink-3);
  transition: background 0.15s ease, color 0.15s ease;
}
.theme-toggle:hover {
  background: var(--hover);
  color: var(--ink);
}
.theme-toggle svg {
  width: 18px;
  height: 18px;
}

.content {
  max-width: 620px;
  margin: 0 auto;
  padding: 32px 16px 72px;
}
.card {
  background: var(--surface);
  border: 1px solid var(--border);
  border-radius: 14px;
  padding: 24px;
}
h1 {
  font-size: 16.5px;
  font-weight: 800;
  margin: 0 0 8px;
}
.status {
  margin: 12px 0;
  padding-left: 22px;
}
.refresh {
  font-size: 14.5px;
  font-weight: 600;
  color: var(--btn-fg);
  background: var(--btn-bg);
  padding: 8px 22px;
  border-radius: 9px;
  transition: background 0.15s ease;
}
.refresh:hover {
  background: var(--btn-hover);
}
.hint {
  margin-top: 16px;
  color: var(--ink-3);
  font-size: 13px;
}
code {
  background: var(--soft);
  border-radius: 5px;
  padding: 1px 6px;
  font-size: 0.9em;
}
</style>
