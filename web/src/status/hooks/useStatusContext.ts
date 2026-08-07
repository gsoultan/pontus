import { useMemo } from 'react'
import { useProjects } from '../../projects/useProjects'
import { useProjectStore } from '../../store/useProjectStore'

/** Resolves the selected project and proxy into the objects the views label with. */
export function useStatusContext() {
  const { data } = useProjects()
  const selectedProjectId = useProjectStore((s) => s.selectedProjectId)
  const selectedProxyId = useProjectStore((s) => s.selectedProxyId)

  return useMemo(() => {
    const project = data?.projects.find((candidate) => candidate.id === selectedProjectId)
    const proxy = project?.proxies.find((candidate) => candidate.id === selectedProxyId)
    return { project, proxy, selectedProjectId, selectedProxyId }
  }, [data, selectedProjectId, selectedProxyId])
}
