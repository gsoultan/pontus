import { Group, Badge, Divider } from "@mantine/core";
import { IconRefresh, IconCrown, IconDatabase } from "@tabler/icons-react";
import { ProxySelector } from "../../projects/ProxySelector";
import { memo } from "react";

interface StatusHeaderActionsProps {
  inFailover: boolean;
  leaderId: string;
  protocol: string;
}

export const StatusHeaderActions = memo(({ inFailover, leaderId, protocol }: StatusHeaderActionsProps) => {
  return (
    <Group gap="md">
      <ProxySelector />
      <Divider orientation="vertical" h={24} />
      <Group gap="xs">
        {inFailover && (
          <Badge variant="filled" color="red" size="lg" radius="md" leftSection={<IconRefresh size={14} stroke={2} />}>
            FAILOVER ACTIVE
          </Badge>
        )}
        <Badge 
          variant="light" 
          color="pontusBlue" 
          size="lg" 
          radius="md" 
          h={36}
          leftSection={<IconCrown size={16} stroke={2} />}
          styles={{ label: { fontWeight: 800 } }}
        >
          Leader: {leaderId}
        </Badge>
        <Badge 
          variant="default" 
          size="lg" 
          radius="md" 
          h={36}
          leftSection={<IconDatabase size={16} stroke={2} />}
          styles={{ label: { fontWeight: 700 } }}
        >
          {protocol.toUpperCase()}
        </Badge>
      </Group>
    </Group>
  );
});

StatusHeaderActions.displayName = 'StatusHeaderActions';
