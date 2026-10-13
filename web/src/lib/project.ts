import { inject, provide, type InjectionKey, type Ref } from 'vue'
import type { Project } from './api'

interface ProjectContext {
  pid: Ref<number>
  project: Ref<Project | null>
  reload: () => Promise<void>
}

const key: InjectionKey<ProjectContext> = Symbol('project')

export function provideProject(ctx: ProjectContext) {
  provide(key, ctx)
}

export function useProject(): ProjectContext {
  const ctx = inject(key)
  if (!ctx) throw new Error('useProject() called outside a project route')
  return ctx
}
