import { useState } from "react";
import {
  TextInput,
  PasswordInput,
  Button,
  Paper,
  Text,
  Title,
  Group,
  Stack,
  Box,
  List,
  ThemeIcon,
  rem,
} from "@mantine/core";
import { useForm } from "@tanstack/react-form";
import { notifications } from "@mantine/notifications";
import { IconCheck, IconDatabase } from "@tabler/icons-react";
import { createClient } from "@connectrpc/connect";
import { ManagementService } from "../gen/api/proto/service/management_pb";
import { transport } from "../services/transport";
import { useAuthStore } from "../store/useAuthStore";
import { useNavigate } from "@tanstack/react-router";
import { motion } from "motion/react";

const MotionStack = motion.create(Stack);
const MotionListItem = motion.create(List.Item);

const containerVariants = {
  hidden: { opacity: 0 },
  visible: {
    opacity: 1,
    transition: { staggerChildren: 0.2, delayChildren: 0.5 },
  },
};

const itemVariants = {
  hidden: { opacity: 0, x: -20 },
  visible: { opacity: 1, x: 0, transition: { duration: 0.5 } },
};

const FEATURES = [
  {
    title: "High-performance Proxy",
    desc: "Intelligent connection pooling and query routing.",
  },
  {
    title: "Real-time Observability",
    desc: "Live query analysis and granular metrics.",
  },
  {
    title: "Cluster Management",
    desc: "Provision and manage globally distributed nodes.",
  },
];

export function LoginPage() {
  const [loading, setLoading] = useState(false);
  const setAuth = useAuthStore((state) => state.setAuth);
  const navigate = useNavigate();

  const form = useForm({
    defaultValues: { username: "", password: "" },
    onSubmit: async ({ value }) => {
      setLoading(true);
      try {
        const client = createClient(ManagementService, transport);
        const response = await client.login(value);

        setAuth(response.token, response.username, response.role);
        notifications.show({
          title: "Login successful",
          message: `Welcome back, ${response.username}!`,
          color: "green",
          icon: <IconCheck size={18} />,
        });
        navigate({ to: "/" });
      } catch (error) {
        notifications.show({
          title: "Login failed",
          message:
            error instanceof Error
              ? error.message
              : "Invalid username or password",
          color: "red",
        });
      } finally {
        setLoading(false);
      }
    },
  });

  return (
    <Box
      style={{
        display: "flex",
        height: "100vh",
        backgroundColor: "light-dark(var(--mantine-color-gray-0), var(--mantine-color-dark-9))",
        overflow: "hidden",
        position: "relative",
      }}
    >
      {/* Background Decorative Elements */}
      <Box visibleFrom="md" style={{ position: "absolute", inset: 0, overflow: "hidden", pointerEvents: "none" }}>
        <Box
          component={motion.div}
          animate={{
            scale: [1, 1.2, 1],
            rotate: [0, 90, 0],
            opacity: [0.1, 0.2, 0.1],
          }}
          transition={{ duration: 20, repeat: Infinity, ease: "linear" }}
          style={{
            position: "absolute",
            top: "-10%",
            left: "-5%",
            width: "40%",
            height: "60%",
            background: "radial-gradient(circle, var(--mantine-color-pontusBlue-4) 0%, transparent 70%)",
            filter: "blur(80px)",
            zIndex: 0,
          }}
        />
        <Box
          component={motion.div}
          animate={{
            scale: [1.2, 1, 1.2],
            rotate: [0, -90, 0],
            opacity: [0.05, 0.15, 0.05],
          }}
          transition={{ duration: 25, repeat: Infinity, ease: "linear" }}
          style={{
            position: "absolute",
            bottom: "-10%",
            right: "40%",
            width: "50%",
            height: "70%",
            background: "radial-gradient(circle, var(--mantine-color-pontusBlue-3) 0%, transparent 70%)",
            filter: "blur(100px)",
            zIndex: 0,
          }}
        />
      </Box>

      {/* Left Column: Branding & Info */}
      <Box
        component={motion.div}
        initial={{ opacity: 0, x: -50 }}
        animate={{ opacity: 1, x: 0 }}
        transition={{ duration: 0.8 }}
        style={{
          flex: 1,
          background: "linear-gradient(135deg, var(--mantine-color-pontusBlue-7) 0%, var(--mantine-color-pontusBlue-9) 100%)",
          color: "white",
          display: "flex",
          flexDirection: "column",
          justifyContent: "center",
          padding: rem(60),
          position: "relative",
          zIndex: 1,
        }}
        visibleFrom="md"
      >
        <MotionStack
          variants={containerVariants}
          initial="hidden"
          animate="visible"
          gap="lg"
          maw={480}
        >
          <motion.div
            animate={{ y: [0, -5, 0] }}
            transition={{ duration: 5, repeat: Infinity, ease: "easeInOut" }}
          >
            <Group gap="sm">
              <ThemeIcon size="xl" radius="md" variant="white" color="pontusBlue">
                <IconDatabase size={24} />
              </ThemeIcon>
              <Box>
                <Title order={1} size="h1" fw={700} style={{ letterSpacing: rem(-1), lineHeight: 1 }}>
                  PONTUS
                </Title>
                <Text size="xs" fw={600} opacity={0.7}>
                  Infrastructure
                </Text>
              </Box>
            </Group>
          </motion.div>

          <motion.div variants={itemVariants}>
            <Text size="lg" fw={500} opacity={0.9} style={{ lineHeight: 1.5 }}>
              The modern database proxy platform for cloud architectures.
            </Text>
          </motion.div>

          <List
            spacing="md"
            size="sm"
            icon={
              <ThemeIcon color="white" size={20} radius="xl">
                <IconCheck size={14} color="var(--mantine-color-pontusBlue-7)" />
              </ThemeIcon>
            }
          >
            {FEATURES.map((feature) => (
              <MotionListItem key={feature.title} variants={itemVariants}>
                <Box>
                  <Text fw={600} size="md">{feature.title}</Text>
                  <Text opacity={0.8}>{feature.desc}</Text>
                </Box>
              </MotionListItem>
            ))}
          </List>
        </MotionStack>
      </Box>

      {/* Right Column: Login Form */}
      <Box
        component={motion.div}
        initial={{ opacity: 0, x: 50 }}
        animate={{ opacity: 1, x: 0 }}
        transition={{ duration: 0.8 }}
        style={{
          flex: 1,
          display: "flex",
          alignItems: "center",
          justifyContent: "center",
          padding: rem(24),
        }}
      >
        <Paper radius="md" p="xl" withBorder style={{ width: "100%", maxWidth: rem(400) }}>
          <Stack gap="xs" mb="lg" align="center">
            <Box hiddenFrom="md">
              <Group gap="xs" mb="md">
                <ThemeIcon size="lg" radius="md" color="pontusBlue">
                  <IconDatabase size={20} />
                </ThemeIcon>
                <Title order={2} fw={700} style={{ letterSpacing: rem(-0.5) }}>
                  PONTUS
                </Title>
              </Group>
            </Box>
            <Title order={2} fw={700}>
              Sign In
            </Title>
            <Text c="dimmed" size="sm">
              Enter your credentials to access the platform
            </Text>
          </Stack>

          <form
            onSubmit={(event) => {
              event.preventDefault();
              event.stopPropagation();
              void form.handleSubmit();
            }}
          >
            <Stack gap="sm">
              <form.Field
                name="username"
                validators={{
                  onSubmit: ({ value }) =>
                    value.trim() ? undefined : "Username is required",
                }}
              >
                {(field) => (
                  <TextInput
                    label="Username"
                    placeholder="Username"
                    required
                    autoComplete="username"
                    value={field.state.value}
                    onChange={(event) => field.handleChange(event.currentTarget.value)}
                    onBlur={field.handleBlur}
                    error={field.state.meta.errors.join(", ") || undefined}
                  />
                )}
              </form.Field>

              <form.Field
                name="password"
                validators={{
                  onSubmit: ({ value }) =>
                    value ? undefined : "Password is required",
                }}
              >
                {(field) => (
                  <PasswordInput
                    label="Password"
                    placeholder="Password"
                    required
                    autoComplete="current-password"
                    value={field.state.value}
                    onChange={(event) => field.handleChange(event.currentTarget.value)}
                    onBlur={field.handleBlur}
                    error={field.state.meta.errors.join(", ") || undefined}
                  />
                )}
              </form.Field>

              <Button type="submit" loading={loading} fullWidth mt="md">
                Sign In
              </Button>
            </Stack>
          </form>

          <Text c="dimmed" size="xs" ta="center" mt="xl">
            © 2026 Pontus.
          </Text>
        </Paper>
      </Box>
    </Box>
  );
}
