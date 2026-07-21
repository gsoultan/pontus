import { Select, Loader } from '@mantine/core';
import { IconActivity } from '@tabler/icons-react';
import { useProjects } from './useProjects';
import { useProjectStore } from '../store/useProjectStore';
import { useEffect, useMemo } from 'react';

export function ProxySelector() {
  const { data, isLoading } = useProjects();
  const { selectedProjectId, selectedProxyId, setSelectedProxyId } = useProjectStore();

  const selectedProject = useMemo(() => 
    data?.projects.find(p => p.id === selectedProjectId),
  [data, selectedProjectId]);

  const proxyOptions = useMemo(() => 
    (selectedProject?.proxies || []).map(p => ({
      value: p.id,
      label: p.name
    })),
  [selectedProject]);

  useEffect(() => {
    if (selectedProjectId && !selectedProxyId && proxyOptions.length > 0) {
      setSelectedProxyId(proxyOptions[0].value);
    }
  }, [selectedProjectId, selectedProxyId, proxyOptions, setSelectedProxyId]);

  if (isLoading) return <Loader size="xs" />;
  if (!selectedProjectId) return null;

  return (
    <Select
      placeholder="Select Proxy"
      data={proxyOptions}
      value={selectedProxyId}
      onChange={setSelectedProxyId}
      leftSection={<IconActivity size={16} />}
      size="sm"
      style={{ width: 180 }}
      disabled={proxyOptions.length === 0}
    />
  );
}
