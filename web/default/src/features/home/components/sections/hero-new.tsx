/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { Link } from '@tanstack/react-router'
import { ArrowRight } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { Button } from '@/components/ui/button'
import { useStatus } from '@/hooks/use-status'
import { useSystemConfig } from '@/hooks/use-system-config'

interface TagItem {
  key: string
  highlighted?: boolean
}

const TAGS: TagItem[] = [
  { key: '40+ Providers', highlighted: true },
  { key: 'Unified API' },
  { key: 'Unified Billing' },
  { key: 'Unified Management' },
  { key: 'Open Source', highlighted: true },
]

const PROVIDERS = [
  'DeepSeek',
  'Qwen',
  'GLM',
  'Kimi',
  'MiniMax',
  'MiMo'
]

interface HeroNewProps {
  isAuthenticated?: boolean
}

export function HeroNew({ isAuthenticated = false }: HeroNewProps) {
  const { t } = useTranslation()
  const { status } = useStatus()
  const docsUrl =
    (status?.docs_link as string | undefined) || 'https://docs.newapi.pro'
  const { systemName } = useSystemConfig()

  return (
    <section className='relative flex min-h-svh flex-col items-center justify-center overflow-hidden px-6'>
      <div className='w-full max-w-3xl text-center'>
        <h1
          className='landing-animate-fade-up text-[clamp(2.25rem,5.5vw,3.75rem)] font-bold leading-[1.1] tracking-tight text-landing-title opacity-0'
          style={{ animationDelay: '0ms' }}
        >
          {t('Secure & Trusted Unified AI Gateway')}
        </h1>

        <p
          className='landing-animate-fade-up mt-5 text-[clamp(1rem,2vw,1.25rem)] font-medium text-landing-primary opacity-0'
          style={{ animationDelay: '100ms' }}
        >
          {t('One API, All AI Models')}
        </p>

        <div
          className='landing-animate-fade-up mt-10 flex flex-wrap items-center justify-center gap-3 opacity-0'
          style={{ animationDelay: '200ms' }}
        >
          {TAGS.map((tag) => (
            <span
              key={tag.key}
              className={
                tag.highlighted
                  ? 'rounded-full bg-landing-primary px-5 py-2 text-[13px] font-medium text-landing-primary-foreground shadow-sm'
                  : 'rounded-full border border-landing-tag-border bg-white px-5 py-2 text-[13px] font-medium text-landing-body shadow-sm dark:bg-landing-hero-bg-start'
              }
            >
              {t(tag.key)}
            </span>
          ))}
        </div>

        <div
          className='landing-animate-fade-up mt-10 flex flex-wrap items-center justify-center gap-3 opacity-0'
          style={{ animationDelay: '300ms' }}
        >
          {isAuthenticated ? (
            <Button
              className='h-12 rounded-xl bg-gradient-to-r from-landing-primary to-landing-primary-hover px-7 text-[15px] font-semibold text-landing-primary-foreground shadow-sm'
              render={<Link to='/dashboard' />}
            >
              {t('Go to Dashboard')}
              <ArrowRight className='ml-1.5 size-4' />
            </Button>
          ) : (
            <>
              <Button
                className='h-12 rounded-xl bg-gradient-to-r from-landing-primary to-landing-primary-hover px-7 text-[15px] font-semibold text-landing-primary-foreground shadow-sm'
                render={<Link to='/sign-up' />}
              >
                {t('Get Started Free')}
                <ArrowRight className='ml-1.5 size-4' />
              </Button>
             
            </>
          )}
        </div>

        <p
          className='landing-animate-fade-up mt-16 text-[13px] font-medium text-landing-faded opacity-0'
          style={{ animationDelay: '400ms' }}
        >
          {t('Supported Providers')}
        </p>
        <div
          className='landing-animate-fade-up mt-4 flex flex-wrap items-center justify-center gap-x-4 gap-y-1 text-[15px] font-medium text-landing-muted opacity-0'
          style={{ animationDelay: '500ms' }}
        >
          {PROVIDERS.map((name, i) => (
            <span key={name} className='inline-flex items-center gap-3'>
              {i > 0 && (
                <span
                  aria-hidden
                  className='inline-block size-0.5 rounded-full bg-landing-dot'
                />
              )}
              {name}
            </span>
          ))}
        </div>
      </div>

      <p
        className='landing-animate-fade-up absolute bottom-10 left-0 right-0 text-center text-[11px] text-landing-subtle opacity-0'
        style={{ animationDelay: '550ms' }}
      >
        contact: jintong@trustharbor.group
      </p>
      <p
        className='landing-animate-fade-up absolute bottom-6 left-0 right-0 text-center text-[11px] text-landing-subtle opacity-0'
        style={{ animationDelay: '600ms' }}
      >
        {t('© {{year}} {{siteName}} · Secure AI Gateway', {
          year: new Date().getFullYear(),
          siteName: systemName,
        })}
      </p>
      <p className='absolute bottom-2 left-0 right-0 text-center text-[11px] text-landing-subtle opacity-0 landing-animate-fade-up'
         style={{ animationDelay: '650ms' }}>
        <a href='https://beian.miit.gov.cn/' target='_blank' rel='noopener noreferrer'>
          粤ICP备2026096811号
        </a>
      </p>
    </section>
  )
}
