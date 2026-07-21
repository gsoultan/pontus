import { Modal } from '@mantine/core';
import { BackendForm } from './BackendForm';
import type { BackendInitialValues, BackendSubmitValues } from './BackendForm';

interface BackendModalProps {
  opened: boolean;
  onClose: () => void;
  onSubmit: (values: BackendSubmitValues) => void;
  initialValues?: BackendInitialValues;
  title: string;
  loading?: boolean;
  hasPrimary?: boolean;
}

export function BackendModal({ opened, onClose, onSubmit, initialValues, title, loading, hasPrimary }: BackendModalProps) {
  return (
    <Modal 
      opened={opened} 
      onClose={onClose} 
      title={title} 
      size="lg"
      radius="md"
    >
      <BackendForm 
        initialValues={initialValues} 
        onSubmit={onSubmit} 
        onCancel={onClose} 
        loading={loading}
        hasPrimary={hasPrimary}
      />
    </Modal>
  );
}
