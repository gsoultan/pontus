import { BALANCERS, DEFAULT_BALANCER } from '../../common/balancers';
import { Stack, TextInput, Select } from '@mantine/core';

interface ProxyConfigFormProps {
  values: {
    name: string;
    address: string;
    balancer: string;
  };
  onChange: (values: any) => void;
}

export function ProxyConfigForm({ values, onChange }: ProxyConfigFormProps) {
  return (
    <Stack gap="md">
      <TextInput
        label="Proxy Name"
        placeholder="Production DB Proxy"
        required
        value={values.name}
        onChange={(e) => onChange({ ...values, name: e.target.value })}
      />
      <TextInput
        label="Listen Address"
        placeholder=":5432"
        description="Address for client connections"
        required
        value={values.address}
        onChange={(e) => onChange({ ...values, address: e.target.value })}
      />
      <Select
        label="Balancing Strategy"
        description="How traffic is distributed"
        data={[...BALANCERS]}
        value={values.balancer}
        onChange={(val) => onChange({ ...values, balancer: val || DEFAULT_BALANCER })}
      />
    </Stack>
  );
}
