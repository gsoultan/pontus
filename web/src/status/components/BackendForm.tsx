import { 
  Button, 
  Group, 
  Stack, 
  TextInput, 
  Select, 
  Text, 
  Box, 
  Badge,
  ThemeIcon, 
  Paper, 
  Slider, 
  rem,
  SimpleGrid,
  UnstyledButton,
  PasswordInput
} from "@mantine/core";
import { 
  IconRefresh,
  IconDatabase,
  IconCopy,
  IconAdjustmentsHorizontal
} from "@tabler/icons-react";
import { notifications } from "@mantine/notifications";
import { useState, useEffect } from "react";
import { useBackendManagement } from "../hooks/useBackendManagement";
import { useStatus } from "../hooks/useStatus";
import { POSTGRES_VERSIONS, DEFAULT_POSTGRES_VERSION } from "../constants";

export interface BackendInitialValues {
  address: string;
  role: string;
  weight?: number;
  managedByAgent?: boolean;
  agentAddress?: string;
  agentToken?: string;
  agentConfig?: {
    dataDirectory: string;
    version: string;
    initialDatabase?: string;
    initialUser?: string;
    initialPassword?: string;
    installIfMissing?: boolean;
  }
}

export interface BackendSubmitValues {
  address: string;
  role: string;
  weight: number;
  managedByAgent?: boolean;
  agentAddress?: string;
  agentToken?: string;
  agentConfig?: {
    dataDirectory: string;
    version: string;
    initialDatabase?: string;
    initialUser?: string;
    initialPassword?: string;
    installIfMissing?: boolean;
  }
}

interface BackendFormProps {
  initialValues?: BackendInitialValues;
  onSubmit: (values: BackendSubmitValues) => void;
  loading?: boolean;
  onCancel?: () => void;
  hasPrimary?: boolean;
}

export function BackendForm({ initialValues, onSubmit, loading, onCancel, hasPrimary: propHasPrimary }: BackendFormProps) {
  const { data: status } = useStatus();
  const hasPrimary = propHasPrimary ?? status?.backends?.some(b => b.role === "primary");

  const [address, setAddress] = useState(initialValues?.address || "");
  const [role, setRole] = useState(initialValues?.role || (hasPrimary ? "replica" : "primary"));
  const [weight, setWeight] = useState(initialValues?.weight || 1);
  const [managedByAgent, setManagedByAgent] = useState(initialValues?.managedByAgent || false);
  const [agentAddress, setAgentAddress] = useState(initialValues?.agentAddress || "");
  const [agentToken, setAgentToken] = useState(initialValues?.agentToken || "");
  const [dataDirectory, setDataDirectory] = useState(initialValues?.agentConfig?.dataDirectory || "/var/lib/postgresql/data");
  const [version, setVersion] = useState(initialValues?.agentConfig?.version || DEFAULT_POSTGRES_VERSION);
  const [initialDatabase, setInitialDatabase] = useState(initialValues?.agentConfig?.initialDatabase || "postgres");
  const [initialUser, setInitialUser] = useState(initialValues?.agentConfig?.initialUser || "postgres");
  const [initialPassword, setInitialPassword] = useState(initialValues?.agentConfig?.initialPassword || "");

  const { 
    getAgentInfo, 
    isGettingAgentInfo,
    availableVersions: globalAvailableVersions
  } = useBackendManagement();
  const [availableVersions, setAvailableVersions] = useState<string[]>(POSTGRES_VERSIONS);
  const [postgresNotFound, setPostgresNotFound] = useState(false);
  const [tuningSuggestions, setTuningSuggestions] = useState<any[]>([]);

  useEffect(() => {
    if (globalAvailableVersions.length > 0) {
      setAvailableVersions(prev => Array.from(new Set([...prev, ...globalAvailableVersions])).sort());
    }
  }, [globalAvailableVersions]);

  useEffect(() => {
    if (postgresNotFound && !managedByAgent) {
      setManagedByAgent(true);
    }
  }, [postgresNotFound, managedByAgent]);

  useEffect(() => {
    if (agentAddress && agentAddress.includes(":")) {
      const timer = setTimeout(() => {
        handleDetectVersion();
      }, 1000);
      return () => clearTimeout(timer);
    }
  }, [agentAddress, agentToken]);

  const handleDetectVersion = async () => {
    if (!agentAddress) {
      notifications.show({ title: "Error", message: "Enter agent address first", color: "red" });
      return;
    }

    try {
      const info = await getAgentInfo({ agentAddress, agentToken });
      
      if (info.postgresRunning && info.postgresAddress) {
        setAddress(info.postgresAddress);
        setPostgresNotFound(false);
        notifications.show({
          title: "PostgreSQL Found",
          message: `Detected running PostgreSQL on ${info.postgresAddress}`,
          color: "green"
        });
      } else if (!info.postgresRunning) {
        setPostgresNotFound(true);
        setRole(hasPrimary ? "replica" : "primary");
        notifications.show({
          title: "PostgreSQL Not Found",
          message: `No PostgreSQL instance is running on this node. Suggested role: ${hasPrimary ? "Replica" : "Primary"}.`,
          color: "orange"
        });
      }

      if (info.postgresDataDir) {
        setDataDirectory(info.postgresDataDir);
      }
      if (info.detectedVersion) {
        setVersion(info.detectedVersion);
      }
      if (info.availableVersions && info.availableVersions.length > 0) {
        setAvailableVersions(prev => Array.from(new Set([...prev, ...info.availableVersions])).sort());
      }
      if (info.tuningSuggestions && info.tuningSuggestions.length > 0) {
        setTuningSuggestions(info.tuningSuggestions);
      }
    } catch (e) {
      notifications.show({ 
        title: "Detection Failed", 
        message: e instanceof Error ? e.message : "Could not connect to agent", 
        color: "red" 
      });
    }
  };

  const validateAddress = (addr: string) => {
    if (postgresNotFound && !addr) return null;
    if (!addr) return "Address is required";
    if (!addr.includes(":")) return "Address must include port (e.g., host:port)";
    return null;
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    
    let finalAddress = address;
    if (!finalAddress && agentAddress) {
      const host = agentAddress.split(":")[0];
      finalAddress = `${host}:5432`;
    }

    const validationError = validateAddress(finalAddress);
    if (validationError) {
      notifications.show({ title: "Validation Error", message: validationError, color: "red" });
      return;
    }
    
    onSubmit({ 
      address: finalAddress, 
      role, 
      weight,
      managedByAgent,
      agentAddress: agentAddress || undefined,
      agentToken: agentToken || undefined,
      agentConfig: managedByAgent ? {
        dataDirectory,
        version,
        initialDatabase,
        initialUser,
        initialPassword,
        installIfMissing: postgresNotFound
      } : undefined
    });
  };

  const handleQuickSetup = (type: 'primary' | 'replica') => {
    setRole(type);
    setWeight(type === 'primary' ? 100 : 50);
  };

  return (
    <form onSubmit={handleSubmit}>
      <Stack gap="md">
        {!initialValues && (
          <Box>
            <Text size="sm" fw={600} mb="xs">Quick Setup</Text>
            <SimpleGrid cols={2} spacing="sm">
               <UnstyledButton 
                  onClick={() => handleQuickSetup('primary')}
                  style={{
                    padding: rem(12),
                    borderRadius: 'var(--mantine-radius-md)',
                    border: `${rem(1)} solid ${role === 'primary' ? 'var(--mantine-color-pontusBlue-6)' : 'light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-6))'}`,
                    backgroundColor: role === 'primary' ? 'light-dark(var(--mantine-color-pontusBlue-0), var(--mantine-color-dark-8))' : 'transparent',
                    transition: 'all 150ms ease'
                  }}
               >
                  <Group gap="sm" wrap="nowrap">
                    <ThemeIcon color="pontusBlue" variant={role === 'primary' ? "filled" : "light"} size="lg">
                      <IconDatabase size={18} />
                    </ThemeIcon>
                    <Box>
                      <Text size="sm" fw={600}>Primary</Text>
                      <Text size="xs" c="dimmed">Read & Write</Text>
                    </Box>
                  </Group>
               </UnstyledButton>

               <UnstyledButton 
                  onClick={() => handleQuickSetup('replica')}
                  style={{
                    padding: rem(12),
                    borderRadius: 'var(--mantine-radius-md)',
                    border: `${rem(1)} solid ${role === 'replica' ? 'var(--mantine-color-pontusBlue-6)' : 'light-dark(var(--mantine-color-gray-2), var(--mantine-color-dark-6))'}`,
                    backgroundColor: role === 'replica' ? 'light-dark(var(--mantine-color-pontusBlue-0), var(--mantine-color-dark-8))' : 'transparent',
                    transition: 'all 150ms ease'
                  }}
               >
                  <Group gap="sm" wrap="nowrap">
                    <ThemeIcon color="indigo" variant={role === 'replica' ? "filled" : "light"} size="lg">
                      <IconCopy size={18} />
                    </ThemeIcon>
                    <Box>
                      <Text size="sm" fw={600}>Replica</Text>
                      <Text size="xs" c="dimmed">Read Only</Text>
                    </Box>
                  </Group>
               </UnstyledButton>
            </SimpleGrid>
          </Box>
        )}

        <Paper withBorder p="md" radius="md" bg="light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-8))">
          <Stack gap="sm">
            <Group justify="space-between" align="center">
              <Text size="sm" fw={600}>Agent Connection</Text>
              <Button 
                size="compact-xs" 
                variant="subtle" 
                onClick={handleDetectVersion}
                loading={isGettingAgentInfo}
                leftSection={<IconRefresh size={14} />}
              >
                Scan Node
              </Button>
            </Group>
            <SimpleGrid cols={{ base: 1, sm: 2 }} spacing="sm">
              <TextInput
                label="Agent Address"
                placeholder="127.0.0.1:9091"
                value={agentAddress}
                onChange={(e) => setAgentAddress(e.target.value)}
                required
              />
              <PasswordInput
                label="Agent Token"
                placeholder="Auth token (if any)"
                value={agentToken}
                onChange={(e) => setAgentToken(e.target.value)}
              />
            </SimpleGrid>
            {tuningSuggestions.length > 0 && (
              <Box mt="xs" pt="xs" style={{ borderTop: `${rem(1)} solid var(--mantine-color-gray-2)` }}>
                <Group gap="xs" mb={4}>
                  <IconAdjustmentsHorizontal size={14} color="var(--mantine-color-violet-6)" />
                  <Text size="xs" fw={700} c="violet.9">Tuning Recommendations</Text>
                </Group>
                <SimpleGrid cols={2} spacing="xs">
                  {tuningSuggestions.map((s, i) => (
                    <Box key={i}>
                      <Text size="xs" fw={700}>{s.parameter}: <Badge size="xs" color="violet" variant="light">{s.suggestedValue}</Badge></Text>
                      <Text size="xs" c="dimmed" style={{ fontSize: rem(10) }}>{s.reason}</Text>
                    </Box>
                  ))}
                </SimpleGrid>
              </Box>
            )}
          </Stack>
        </Paper>

        <Box>
           <Text size="sm" fw={600} mb={2}>Selection Weight</Text>
           <Text size="xs" c="dimmed" mb="xs">Higher weight increases selection priority</Text>
           <Slider 
             value={weight} 
             onChange={setWeight} 
             min={1} 
             max={100} 
             label={(val) => `${val}%`}
             marks={[
               { value: 1, label: '1' },
               { value: 100, label: '100' },
             ]}
             mb="lg"
           />
        </Box>

        {postgresNotFound && (
          <Paper 
            p="md" 
            radius="md" 
            bg="light-dark(var(--mantine-color-orange-0), rgba(255, 145, 0, 0.1))" 
            withBorder 
            style={{ borderColor: 'light-dark(var(--mantine-color-orange-2), var(--mantine-color-orange-9))' }}
          >
            <Stack gap="sm">
              <Group gap="xs">
                <IconDatabase size={16} color="var(--mantine-color-orange-6)" />
                <Text size="sm" fw={600} c="orange.9">Install PostgreSQL</Text>
              </Group>
              <Text size="xs" c="orange.8" fw={500}>
                PostgreSQL not detected. It will be installed as a <strong>{role}</strong> node.
              </Text>
              <SimpleGrid cols={2} spacing="sm">
                 <Select
                    label="Version"
                    data={availableVersions}
                    value={version}
                    onChange={(val) => setVersion(val || DEFAULT_POSTGRES_VERSION)}
                 />
                 <TextInput
                    label="Data Directory"
                    value={dataDirectory}
                    onChange={(e) => setDataDirectory(e.target.value)}
                 />
              </SimpleGrid>
              <SimpleGrid cols={3} spacing="sm">
                <TextInput
                  label="Initial DB"
                  value={initialDatabase}
                  onChange={(e) => setInitialDatabase(e.target.value)}
                />
                <TextInput
                  label="User"
                  value={initialUser}
                  onChange={(e) => setInitialUser(e.target.value)}
                />
                <PasswordInput
                  label="Password"
                  value={initialPassword}
                  onChange={(e) => setInitialPassword(e.target.value)}
                />
              </SimpleGrid>
            </Stack>
          </Paper>
        )}

        <Group justify="flex-end" mt="md">
          {onCancel && (
            <Button variant="subtle" onClick={onCancel} disabled={loading}>
              Cancel
            </Button>
          )}
          <Button type="submit" loading={loading} px="xl">
            {initialValues ? "Save Changes" : "Register Backend"}
          </Button>
        </Group>
      </Stack>
    </form>
  );
}

