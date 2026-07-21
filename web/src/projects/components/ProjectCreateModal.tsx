import { Modal, Stack, TextInput, Select, Button, Text } from '@mantine/core';
import { useState } from 'react';

interface ProjectCreateModalProps {
  opened: boolean;
  onClose: () => void;
  onSubmit: (values: { name: string; proxyAddr: string; protocol: string }) => void;
  isPending: boolean;
}

export function ProjectCreateModal({ opened, onClose, onSubmit, isPending }: ProjectCreateModalProps) {
  const [form, setForm] = useState({
    name: '',
    proxyAddr: '',
    protocol: 'postgres'
  });

  const handleSubmit = () => {
    onSubmit(form);
    setForm({ name: '', proxyAddr: '', protocol: 'postgres' });
  };

  return (
    <Modal opened={opened} onClose={onClose} title={<Text fw={700}>Create New Project</Text>} centered>
      <Stack>
        <TextInput
          label="Project Name"
          placeholder="e.g. E-commerce Production"
          required
          value={form.name}
          onChange={(e) => setForm({ ...form, name: e.target.value })}
        />
        <Select
          label="Protocol"
          data={['postgres', 'mysql']}
          value={form.protocol}
          onChange={(val) => setForm({ ...form, protocol: val || 'postgres' })}
        />
        <TextInput
          label="Initial Proxy Address (Optional)"
          placeholder="e.g. :5432"
          value={form.proxyAddr}
          onChange={(e) => setForm({ ...form, proxyAddr: e.target.value })}
        />
        <Button onClick={handleSubmit} loading={isPending} fullWidth mt="md">
          Create Project
        </Button>
      </Stack>
    </Modal>
  );
}
