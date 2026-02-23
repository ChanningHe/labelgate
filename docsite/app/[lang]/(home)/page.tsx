import Link from 'next/link';

const features = {
  en: [
    {
      title: 'Automated DNS Records',
      description:
        'Create and manage Cloudflare DNS records automatically. Support for A, AAAA, CNAME, TXT, MX, SRV, and CAA record types.',
    },
    {
      title: 'Tunnel Ingress Rules',
      description:
        'Automatically configure Cloudflare Tunnel ingress rules. Expose your services securely without opening ports.',
    },
    {
      title: 'Zero Trust Access',
      description:
        'Manage Cloudflare Access policies through labels. Control who can access your applications with ease.',
    },
    {
      title: 'Multi-Host Agent Mode',
      description:
        'Monitor Docker containers across multiple hosts via WebSocket-based agent architecture.',
    },
    {
      title: 'Flexible Configuration',
      description:
        'Configure via environment variables, YAML files, or both. Support for multiple Cloudflare API tokens.',
    },
    {
      title: 'State Persistence',
      description:
        'Track all managed resources in SQLite. Detect conflicts, prevent duplicates, and recover gracefully.',
    },
  ],
  zh: [
    {
      title: '自动化 DNS 记录',
      description:
        '自动创建和管理 Cloudflare DNS 记录。支持 A、AAAA、CNAME、TXT、MX、SRV 和 CAA 记录类型。',
    },
    {
      title: 'Tunnel 入站规则',
      description:
        '自动配置 Cloudflare Tunnel 入站规则。无需开放端口即可安全暴露服务。',
    },
    {
      title: 'Zero Trust 访问控制',
      description:
        '通过标签管理 Cloudflare Access 策略。轻松控制谁可以访问您的应用程序。',
    },
    {
      title: '多主机 Agent 模式',
      description:
        '通过基于 WebSocket 的 Agent 架构，监控跨多台主机的 Docker 容器。',
    },
    {
      title: '灵活配置',
      description:
        '支持环境变量、YAML 文件或两者混合配置。支持多个 Cloudflare API Token。',
    },
    {
      title: '状态持久化',
      description:
        '在 SQLite 中追踪所有托管资源。检测冲突、防止重复、优雅恢复。',
    },
  ],
};

const texts = {
  en: {
    tagline: 'Automate Cloudflare DNS, Tunnels, and Access Policies through Docker Labels',
    getStarted: 'Get Started',
    viewOnGitHub: 'GitHub',
    exampleTitle: 'Simple as a Label',
    exampleDescription: 'Add labels to your Docker containers and Labelgate handles the rest.',
  },
  zh: {
    tagline: '通过 Docker Labels 自动管理 Cloudflare DNS、Tunnel 和 Access Policy',
    getStarted: '开始使用',
    viewOnGitHub: 'GitHub',
    exampleTitle: '简单如一行标签',
    exampleDescription: '给 Docker 容器添加标签，Labelgate 自动完成剩余工作。',
  },
};

const exampleYaml = `services:
  labelgate:
    image: labelgate:latest
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
    environment:
      - LABELGATE_CLOUDFLARE_API_TOKEN=\${CF_API_TOKEN}
      - LABELGATE_CLOUDFLARE_ACCOUNT_ID=\${CF_ACCOUNT_ID}
      - LABELGATE_CLOUDFLARE_TUNNEL_ID=\${CF_TUNNEL_ID}

  webapp:
    image: nginx
    labels:
      labelgate.tunnel.web.hostname: "app.example.com"
      labelgate.tunnel.web.service: "http://webapp:80"`;

export default async function LandingPage({
  params,
}: {
  params: Promise<{ lang: string }>;
}) {
  const { lang } = await params;
  const l = lang === 'zh' ? 'zh' : 'en';
  const t = texts[l];
  const f = features[l];
  const getStartedUrl = l === 'en' ? '/docs/getting-started' : `/${l}/docs/getting-started`;

  return (
    <main>
      {/* Hero Section */}
      <section className="flex flex-col items-center px-6 pt-20 pb-16 text-center md:pt-28 md:pb-20">
        <h1 className="mb-6 text-5xl font-extrabold tracking-tight md:text-6xl">
          Labelgate
        </h1>
        <p className="mb-10 max-w-2xl text-lg text-fd-muted-foreground md:text-xl">
          {t.tagline}
        </p>
        <div className="flex gap-4">
          <Link
            href={getStartedUrl}
            className="rounded-lg bg-fd-primary px-6 py-3 font-medium text-fd-primary-foreground transition-colors hover:bg-fd-primary/90"
          >
            {t.getStarted}
          </Link>
          <Link
            href="https://github.com/channinghe/labelgate"
            target="_blank"
            rel="noopener noreferrer"
            className="rounded-lg border border-fd-border px-6 py-3 font-medium transition-colors hover:bg-fd-accent"
          >
            {t.viewOnGitHub}
          </Link>
        </div>
      </section>

      {/* Quick Example Section */}
      <section className="mx-auto max-w-4xl px-6 py-16">
        <h2 className="mb-3 text-center text-3xl font-bold">{t.exampleTitle}</h2>
        <p className="mb-8 text-center text-fd-muted-foreground">
          {t.exampleDescription}
        </p>
        <div className="overflow-hidden rounded-xl border border-fd-border">
          <div className="border-b border-fd-border bg-fd-card px-4 py-2 text-sm text-fd-muted-foreground">
            docker-compose.yaml
          </div>
          <pre className="overflow-x-auto bg-fd-background p-4 text-sm">
            <code>{exampleYaml}</code>
          </pre>
        </div>
      </section>

      {/* Features Section */}
      <section className="mx-auto max-w-6xl px-6 py-16">
        <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-3">
          {f.map((feature) => (
            <div
              key={feature.title}
              className="rounded-xl border border-fd-border bg-fd-card p-6 transition-colors hover:bg-fd-accent/50"
            >
              <h3 className="mb-2 text-lg font-semibold">{feature.title}</h3>
              <p className="text-sm text-fd-muted-foreground">
                {feature.description}
              </p>
            </div>
          ))}
        </div>
      </section>

      {/* Footer */}
      <footer className="border-t border-fd-border px-6 py-8 text-center text-sm text-fd-muted-foreground">
        <p>
          &copy; {new Date().getFullYear()} Labelgate.{' '}
          {l === 'zh' ? '基于 MIT 许可证开源。' : 'Released under the MIT License.'}
        </p>
      </footer>
    </main>
  );
}
