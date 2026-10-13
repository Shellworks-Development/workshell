import { ref } from 'vue'

export const paletteOpen = ref(false)

export function openPalette() {
  paletteOpen.value = true
}
