// 主题切换（docs/DESIGN_SYSTEM.md §8）：data-theme + localStorage 键 tl-theme
// 首帧由 index.html 内联脚本按白名单处理；这里维护主题注册表与运行期切换。
// 注意：swatch 预览色点为主题元数据（与 tokens.css 各主题令牌值保持一致），
// 仅经选择器内联 style 渲染，不进入组件 CSS，不受「组件禁止字面量颜色」约束。
import { ref } from 'vue'

export type ThemeId = 'light' | 'dark' | 'eva-01' | 'eva-rei' | 'eva-asuka'

export interface ThemeMeta {
  id: ThemeId
  /** 选择器展示用的中文名 */
  label: string
  /** 预览色点（背景 + 主色 [+ 点缀色]） */
  swatch: string[]
}

/** 主题注册表：新增主题 = 这里加一项 + tokens.css 加一块同 id 令牌 */
export const THEMES: ThemeMeta[] = [
  { id: 'light', label: '浅色', swatch: ['#ffffff', '#2b63f2'] },
  { id: 'dark', label: '深色', swatch: ['#151b23', '#4493f8'] },
  { id: 'eva-01', label: 'EVA 初号机', swatch: ['#1a1524', '#8b63e8', '#9fe32e'] },
  { id: 'eva-rei', label: 'EVA 凌波丽', swatch: ['#eef3f7', '#4d7fa3', '#c9455e'] },
  { id: 'eva-asuka', label: 'EVA 明日香', swatch: ['#faf4ee', '#d8433c', '#5f8a2e'] },
]

const THEME_IDS = new Set<string>(THEMES.map((item) => item.id))

/** 非法或缺失的值回退浅色（跟随系统的回退只发生在 index.html 首帧） */
function normalize(id: string | null): ThemeId {
  return id !== null && THEME_IDS.has(id) ? (id as ThemeId) : 'light'
}

const theme = ref<ThemeId>(normalize(document.documentElement.getAttribute('data-theme')))

export function useTheme() {
  function setTheme(id: ThemeId): void {
    theme.value = id
    document.documentElement.setAttribute('data-theme', id)
    try {
      localStorage.setItem('tl-theme', id)
    } catch {
      /* 忽略存储失败 */
    }
  }

  return { theme, themes: THEMES, setTheme }
}
