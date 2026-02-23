import { Outlet, NavLink, useLocation } from 'react-router-dom';
import {
  AppShell,
  Group,
  Text,
  NavLink as MantineNavLink,
  ScrollArea,
  ActionIcon,
  useMantineColorScheme,
  useComputedColorScheme,
  Divider,
  ThemeIcon,
  Stack,
  Burger,
  Tooltip,
} from '@mantine/core';
import { useDisclosure } from '@mantine/hooks';
import {
  IconLayoutDashboard,
  IconWorldWww,
  IconArrowsTransferDown,
  IconShieldLock,
  IconServer,
  IconSun,
  IconMoon,
  IconTagsFilled,
  IconBrandGithub,
} from '@tabler/icons-react';

const navItems = [
  { label: 'Overview', icon: IconLayoutDashboard, to: '/' },
];

const resourceItems = [
  { label: 'DNS', icon: IconWorldWww, to: '/dns' },
  { label: 'Tunnels', icon: IconArrowsTransferDown, to: '/tunnels' },
  { label: 'Access', icon: IconShieldLock, to: '/access' },
];

const systemItems = [
  { label: 'Agents', icon: IconServer, to: '/agents' },
];

const GITHUB_URL = 'https://github.com/channinghe/labelgate';
const HEADER_HEIGHT = 48;

export function AppLayout() {
  const [opened, { toggle, close }] = useDisclosure();
  const { colorScheme, toggleColorScheme } = useMantineColorScheme();
  const computedColorScheme = useComputedColorScheme('light');
  const location = useLocation();

  const isActive = (to: string) => {
    if (to === '/') return location.pathname === '/';
    return location.pathname.startsWith(to);
  };

  const renderNavItems = (items: typeof navItems) =>
    items.map((item) => (
      <MantineNavLink
        key={item.to}
        component={NavLink}
        to={item.to}
        label={item.label}
        leftSection={<item.icon size={18} stroke={1.5} />}
        active={isActive(item.to)}
        variant="light"
        onClick={close}
        style={{ borderRadius: 'var(--mantine-radius-md)' }}
      />
    ));

  return (
    <AppShell
      header={{ height: HEADER_HEIGHT }}
      navbar={{ width: 260, breakpoint: 'sm', collapsed: { mobile: !opened } }}
      padding="lg"
      styles={{
        header: {
          borderBottom: 'none',
          backgroundColor: 'transparent',
        },
        navbar: {
          borderRight: 'none',
          boxShadow: 'var(--mantine-shadow-sm)',
          // Sidebar starts from the very top, ignoring the header offset
          top: 0,
          height: '100vh',
        },
      }}
    >
      {/* Thin header: burger on mobile, action buttons on desktop */}
      <AppShell.Header>
        <Group h="100%" px="md" justify="space-between">
          {/* Mobile burger */}
          <Group hiddenFrom="sm">
            <Burger opened={opened} onClick={toggle} size="sm" />
          </Group>
          {/* Empty spacer on desktop so buttons align right */}
          <span />

          {/* GitHub + theme toggle — top-right corner */}
          <Group gap="xs">
            <Tooltip label="GitHub" withArrow>
              <ActionIcon
                variant="subtle"
                color="gray"
                size="lg"
                component="a"
                href={GITHUB_URL}
                target="_blank"
                rel="noopener noreferrer"
                aria-label="GitHub repository"
              >
                <IconBrandGithub size={18} />
              </ActionIcon>
            </Tooltip>
            <Tooltip label={colorScheme === 'dark' ? 'Light mode' : 'Dark mode'} withArrow>
              <ActionIcon
                variant="subtle"
                color="gray"
                size="lg"
                onClick={toggleColorScheme}
                aria-label="Toggle color scheme"
              >
                {colorScheme === 'dark' ? <IconSun size={18} /> : <IconMoon size={18} />}
              </ActionIcon>
            </Tooltip>
          </Group>
        </Group>
      </AppShell.Header>

      <AppShell.Navbar
        p="md"
        bg={computedColorScheme === 'dark' ? 'dark.7' : 'gray.0'}
      >
        {/* Logo + title at top of sidebar */}
        <AppShell.Section>
          <Group gap="sm" mb="xl" px={4} justify="space-between">
            <Group gap="sm">
              <ThemeIcon variant="filled" color="orange" size={42} radius="md">
                <IconTagsFilled size={24} />
              </ThemeIcon>
              <Stack gap={0}>
                <Text fw={800} size="xl" lh={1.2}>
                  Labelgate
                </Text>
                <Text size="xs" c="dimmed" lh={1}>
                  Dashboard
                </Text>
              </Stack>
            </Group>
            {/* Close button on mobile */}
            <Burger opened={opened} onClick={toggle} size="sm" hiddenFrom="sm" />
          </Group>
        </AppShell.Section>

        <AppShell.Section grow component={ScrollArea}>
          {renderNavItems(navItems)}

          <Divider my="sm" />
          <Text size="xs" fw={600} c="dimmed" px="sm" mb={6} mt={4} tt="uppercase" lts={0.5}>
            Resources
          </Text>
          {renderNavItems(resourceItems)}

          <Divider my="sm" />
          <Text size="xs" fw={600} c="dimmed" px="sm" mb={6} mt={4} tt="uppercase" lts={0.5}>
            System
          </Text>
          {renderNavItems(systemItems)}
        </AppShell.Section>

        <AppShell.Section>
          <Divider mb="sm" />
          <Group px="sm" pb="xs" justify="center">
            <Text size="xs" c="dimmed">
              Labelgate
            </Text>
          </Group>
        </AppShell.Section>
      </AppShell.Navbar>

      <AppShell.Main>
        <Outlet />
      </AppShell.Main>
    </AppShell>
  );
}
