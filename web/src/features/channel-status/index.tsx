/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { Link } from '@tanstack/react-router'
import { Activity, Clock3, RefreshCw, Settings2 } from 'lucide-react'
import { useTranslation } from 'react-i18next'

import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import { toIntlLocale } from '@/i18n/languages'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { StatusSummary } from './components/status-summary'
import { StatusTable } from './components/status-table'
import { useChannelStatus } from './hooks/use-channel-status'
import { formatTimestamp } from './lib/format'

export function ChannelStatusPage() {
  const { t, i18n } = useTranslation()
  const isAdmin = useAuthStore(
    (state) => (state.auth.user?.role ?? 0) >= ROLE.ADMIN
  )
  const query = useChannelStatus()
  const locale = toIntlLocale(i18n.resolvedLanguage || i18n.language)
  const data = query.data

  let content = <LoadingState className='min-h-[240px]' />
  if (query.isError && !data) {
    content = (
      <ErrorState
        title={t('Unable to load service status')}
        description={t('Check your connection and try again.')}
        onRetry={() => void query.refetch()}
        className='min-h-[240px]'
      />
    )
  } else if (!query.isLoading && data?.targets.length === 0) {
    content = (
      <EmptyState
        icon={Activity}
        title={t('No monitored routes')}
        description={t(
          'No group and model status targets are available for your account.'
        )}
        className='min-h-[240px]'
      />
    )
  } else if (!query.isLoading && data) {
    content = (
      <div className='min-h-0 flex-1 overflow-auto'>
        {query.isError && (
          <div
            role='alert'
            className='border-destructive/30 bg-destructive/5 text-destructive border-b px-3 py-2 text-sm sm:px-4'
          >
            {t('Refresh failed. Showing the last available status.')}
          </div>
        )}
        <StatusSummary targets={data.targets} />
        <StatusTable targets={data.targets} locale={locale} />
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('API service status')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        {isAdmin && (
          <Button
            type='button'
            variant='outline'
            size='icon'
            aria-label={t('Channel status settings')}
            title={t('Channel status settings')}
            render={<Link to='/channel-status/settings' />}
          >
            <Settings2 aria-hidden='true' />
          </Button>
        )}
        <div className='text-muted-foreground hidden items-center gap-1.5 text-xs sm:flex'>
          <Clock3 className='size-3.5' aria-hidden='true' />
          <span>
            {t('Updated')} {formatTimestamp(data?.updated_at, locale)}
          </span>
        </div>
        <Button
          type='button'
          variant='outline'
          size='icon'
          aria-label={t('Refresh status')}
          title={t('Refresh status')}
          onClick={() => void query.refetch()}
          disabled={query.isFetching}
        >
          <RefreshCw
            className={query.isFetching ? 'animate-spin' : undefined}
            aria-hidden='true'
          />
        </Button>
      </SectionPageLayout.Actions>
      <SectionPageLayout.Content>
        <div className='flex h-full min-h-0 flex-col overflow-hidden rounded-md border'>
          {content}
        </div>
      </SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
