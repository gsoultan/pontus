import { ReactFlow, Background, Controls, type Node, type Edge } from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import { useMemo, memo } from 'react';
import { Card, Title, Group, Badge, rem } from '@mantine/core';

interface TopologyGraphProps {
  topology?: {
    nodes: Array<{ id: string; label: string; type: string; status: string; address: string }>;
    edges: Array<{ from: string; to: string; label: string; type: string }>;
  };
}

export const TopologyGraph = memo(({ topology }: TopologyGraphProps) => {
  const nodes: Node[] = useMemo(() => {
    if (!topology?.nodes) return [];
    return topology.nodes.map((n, idx) => ({
      id: n.id,
      data: { label: n.label },
      position: { x: n.type === 'proxy' ? 50 : 350, y: 50 + idx * 100 },
      type: 'default',
      style: {
        background: n.type === 'proxy' ? 'var(--mantine-color-blue-0)' : n.type === 'primary' ? 'var(--mantine-color-successGreen-0)' : 'var(--mantine-color-orange-0)',
        border: `2px solid ${n.status === 'healthy' ? 'var(--mantine-color-successGreen-6)' : 'var(--mantine-color-red-6)'}`,
        borderRadius: '12px',
        padding: '10px',
        width: 180,
        fontWeight: 700,
        boxShadow: 'var(--mantine-shadow-sm)',
        fontSize: rem(12),
        textAlign: 'center' as const,
      },
    }));
  }, [topology]);

  const edges: Edge[] = useMemo(() => {
    if (!topology?.edges) return [];
    return topology.edges.map((e) => ({
      id: `${e.from}-${e.to}`,
      source: e.from,
      target: e.to,
      label: e.label,
      animated: true,
      style: { stroke: 'var(--mantine-color-pontusBlue-5)', strokeWidth: 2 },
      labelStyle: { fill: 'var(--mantine-color-pontusBlue-7)', fontWeight: 800, fontSize: rem(10) },
    }));
  }, [topology]);

  return (
    <Card withBorder shadow="sm" radius="lg" p="xl" style={{ height: 500 }}>
      <Group justify="space-between" mb="md">
        <Title order={4} fw={800}>Infrastructure Topology</Title>
        <Badge variant="light" color="pontusBlue" radius="sm">Real-time Map</Badge>
      </Group>
      <div style={{ height: '400px', width: '100%', borderRadius: 'var(--mantine-radius-md)', overflow: 'hidden' }}>
        <ReactFlow
          nodes={nodes}
          edges={edges}
          fitView
          nodesConnectable={false}
          nodesDraggable={true}
          zoomOnScroll={false}
          panOnDrag={true}
        >
          <Background color="var(--mantine-color-gray-0)" />
          <Controls />
        </ReactFlow>
      </div>
    </Card>
  );
});

TopologyGraph.displayName = 'TopologyGraph';
