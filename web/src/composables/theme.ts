// 主题切换（docs/DESIGN_SYSTEM.md §8）：data-theme + localStorage 键 tl-theme
// 首帧由 index.html 内联脚本处理；这里只负责运行期切换。
import { ref } from 'vue'

const theme = ref<'light' | 'dark'>(
  (document.documentElement.getAttribute('data-theme') as 'light' | 'dark') || 'light',
)

export function useTheme() {
  function toggleTheme(): void {
    theme.value = theme.value === 'dark' ? 'light' : 'dark'
    document.documentElement.setAttribute('data-theme', theme.value)
    try {
      localStorage.setItem('tl-theme', theme.value)
    } catch {
      /* 忽略存储失败 */
    }
  }

  return { theme, toggleTheme }
}
