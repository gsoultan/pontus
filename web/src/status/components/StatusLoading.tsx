import { Container, SimpleGrid, Grid, Paper, Skeleton, Box } from "@mantine/core";
import { PageHeader } from "../../layout/components/PageHeader";

export function StatusLoading() {
  return (
    <Container size="xl" py="xl">
      <Box mb="xl">
        <Skeleton height={40} width={300} mb="md" radius="md" />
        <PageHeader 
          title={<Skeleton height={40} width={400} radius="md" />} 
          description={<Skeleton height={20} width={600} mt="xs" radius="md" />}
          actions={<Skeleton height={40} width={200} radius="md" />}
        />
      </Box>

      <Grid mb="xl" gap="lg">
        {[1, 2, 3, 4].map((i) => (
          <Grid.Col key={i} span={{ base: 12, md: 3 }}>
            <Paper p="xl" radius="lg" withBorder>
              <Skeleton height={12} width="40%" mb="md" />
              <Skeleton height={36} width="60%" mb="sm" />
              <Skeleton height={14} width="80%" />
            </Paper>
          </Grid.Col>
        ))}
      </Grid>

      <SimpleGrid cols={{ base: 1, md: 2 }} spacing="md" mb="xl">
        <Skeleton height={300} radius="lg" />
        <Skeleton height={300} radius="lg" />
      </SimpleGrid>

      <SimpleGrid cols={{ base: 1, lg: 2 }} spacing="lg" mb="xl">
        <Skeleton height={200} radius="lg" />
        <Skeleton height={200} radius="lg" />
      </SimpleGrid>

      <Box mt="xl">
        <Skeleton height={30} width={200} mb="lg" radius="sm" />
        <SimpleGrid cols={{ base: 1, sm: 2, lg: 3 }} spacing="lg">
          {[1, 2, 3].map((i) => (
            <Skeleton key={i} height={250} radius="lg" />
          ))}
        </SimpleGrid>
      </Box>
    </Container>
  );
}
