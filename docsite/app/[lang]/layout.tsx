import '../global.css';
import { RootProvider } from 'fumadocs-ui/provider/next';
import { defineI18nUI } from 'fumadocs-ui/i18n';
import { Inter } from 'next/font/google';
import { i18n } from '@/lib/i18n';
import type { ReactNode } from 'react';
import type { Metadata } from 'next';

export function generateStaticParams() {
  return i18n.languages.map((lang) => ({ lang }));
}

export const metadata: Metadata = {
  icons: { icon: '/favicon.svg' },
};

const inter = Inter({ subsets: ['latin'] });

const { provider } = defineI18nUI(i18n, {
  translations: {
    en: {
      displayName: 'English',
    },
    zh: {
      displayName: '中文',
      search: '搜索文档',
      searchNoResult: '未找到结果',
      toc: '目录',
      tocNoHeadings: '无标题',
      lastUpdate: '最后更新',
      previousPage: '上一页',
      nextPage: '下一页',
      chooseLanguage: '选择语言',
    },
  },
});

export default async function LangLayout({
  params,
  children,
}: {
  params: Promise<{ lang: string }>;
  children: ReactNode;
}) {
  const { lang } = await params;

  return (
    <html lang={lang} suppressHydrationWarning>
      <body className={`${inter.className} flex min-h-screen flex-col`}>
        <RootProvider i18n={provider(lang)}>{children}</RootProvider>
      </body>
    </html>
  );
}
