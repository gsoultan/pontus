import { Modal, Stack, TextInput, Button, Select, Text, Box, Checkbox } from "@mantine/core";
import { IconDownload, IconUpload, IconWand } from "@tabler/icons-react";
import { useState, useMemo } from "react";
import type { BackendStatus } from "../../gen/api/proto/domain/status_pb";

interface MaintenanceModalProps {
  opened: boolean;
  onClose: () => void;
  backends: BackendStatus[];
  onBackup: (params: { address: string; backupPath: string }) => Promise<void>;
  onRestore: (params: { address: string; backupPath: string }) => Promise<void>;
  onVacuum?: (params: { address: string; database: string; full: boolean }) => Promise<void>;
  loading?: boolean;
  type: 'backup' | 'restore' | 'vacuum';
  initialAddress?: string;
}

export function MaintenanceModal({ opened, onClose, backends, onBackup, onRestore, onVacuum, loading, type, initialAddress }: MaintenanceModalProps) {
  const [address, setAddress] = useState("");
  const [path, setPath] = useState("");
  const [database, setDatabase] = useState("postgres");
  const [full, setFull] = useState(false);

  const managedBackends = useMemo(() => backends.filter(b => b.agentAddress), [backends]);

  // Seed the form when the modal opens and clear it when it closes, adjusted
  // on the transition during render. An effect re-ran this whenever
  // initialAddress or the managed-backend list changed while the modal was
  // open, overwriting whatever the operator had selected in the meantime.
  const [wasOpened, setWasOpened] = useState(opened);
  if (opened !== wasOpened) {
    setWasOpened(opened);
    if (opened) {
      if (initialAddress) {
        setAddress(initialAddress);
      } else if (managedBackends.length > 0) {
        setAddress(managedBackends[0].address);
      }
    } else {
      setPath("");
    }
  }

  const handleSubmit = async () => {
    if (!address) return;
    if (type !== 'vacuum' && !path) return;
    
    try {
      if (type === 'backup') {
        await onBackup({ address, backupPath: path });
      } else if (type === 'restore') {
        await onRestore({ address, backupPath: path });
      } else if (type === 'vacuum' && onVacuum) {
        await onVacuum({ address, database, full });
      }
      onClose();
      setPath("");
    } catch {
      // Error is handled by the hook/mutation
    }
  };

  return (
    <Modal 
      opened={opened} 
      onClose={onClose} 
      title={
        type === 'backup' ? "Backup Database" : 
        type === 'restore' ? "Restore Database" : 
        "Vacuum Database"
      }
      centered
      radius="md"
    >
      <Stack>
        <Text size="sm" c="dimmed">
          {type === 'backup' 
            ? "Create a full backup of the selected database node." 
            : type === 'restore'
            ? "Restore the database node from a previous backup file."
            : "Reclaim storage and update statistics on the database node."}
        </Text>

        {managedBackends.length === 0 ? (
          <Box p="md" bg="red.0" style={{ borderRadius: '8px', border: '1px solid var(--mantine-color-red-2)' }}>
            <Text size="sm" c="red" fw={600}>No managed backends found.</Text>
            <Text size="xs" c="red.8">Only backends with Pontus Agent enabled can be backed up or restored through the UI.</Text>
          </Box>
        ) : (
          <>
            <Select 
              label="Select Target Node"
              description="Choose the database instance to operate on"
              placeholder="Choose a node"
              data={managedBackends.map(b => ({ value: b.address, label: `${b.address} (${b.role})` }))}
              value={address}
              onChange={(val) => setAddress(val || "")}
              required
              radius="md"
            />
            {type === 'vacuum' ? (
              <>
                <TextInput 
                  label="Database Name"
                  placeholder="postgres"
                  value={database}
                  onChange={(e) => setDatabase(e.target.value)}
                  required
                  radius="md"
                />
                <Checkbox 
                  label="Full Vacuum"
                  description="More thorough, but takes much longer and locks tables"
                  checked={full}
                  onChange={(e) => setFull(e.currentTarget.checked)}
                />
              </>
            ) : (
              <TextInput 
                label={type === 'backup' ? "Backup Destination Path" : "Source Backup Path"}
                description={type === 'backup' ? "Full path on the host machine" : "Full path to the .sql or .bak file on the host"}
                placeholder="/var/backups/pg_backup.sql"
                value={path}
                onChange={(e) => setPath(e.target.value)}
                required
                radius="md"
              />
            )}
            <Button 
              fullWidth 
              onClick={handleSubmit} 
              loading={loading}
              leftSection={
                type === 'backup' ? <IconDownload size={16} /> : 
                type === 'restore' ? <IconUpload size={16} /> : 
                <IconWand size={16} />
              }
              color={
                type === 'backup' ? "blue" : 
                type === 'restore' ? "orange" : 
                "teal"
              }
              radius="md"
              mt="md"
            >
              {type === 'backup' ? "Start Backup Process" : 
               type === 'restore' ? "Start Restore Process" : 
               "Run Vacuum Now"}
            </Button>
          </>
        )}
      </Stack>
    </Modal>
  );
}
