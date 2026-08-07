import { useQuery } from '@tanstack/react-query'
import { statusClient } from '../../status/services/statusService'

/** Database versions this build knows how to install. Effectively static. */
export function useAvailableVersions() {
  return useQuery({
    queryKey: ['availableVersions'],
    queryFn: async () => (await statusClient.getAvailableVersions({})).versions,
    staleTime: 1000 * 60 * 60,
  })
}
