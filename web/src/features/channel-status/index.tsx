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
import { Activity, Clock3, RefreshCw, Search, Settings2 } from 'lucide-react'
import { useState } from 'react'
import { useTranslation } from 'react-i18next'

import type { DataTableViewMode } from '@/components/data-table/hooks/use-data-table-view-mode'
import { DataTableViewModeToggle } from '@/components/data-table/toolbar/view-mode-toggle'
import { EmptyState } from '@/components/empty-state'
import { ErrorState } from '@/components/error-state'
import { SectionPageLayout } from '@/components/layout'
import { LoadingState } from '@/components/loading-state'
import { Button } from '@/components/ui/button'
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
} from '@/components/ui/input-group'
import { toIntlLocale } from '@/i18n/languages'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

import { StatusCards } from './components/status-cards'
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
  const [search, setSearch] = useState('')
  const [view, setView] = useState<DataTableViewMode>('card')
  const normalizedSearch = search.trim().toLocaleLowerCase(locale)
  const visibleTargets = (data?.targets ?? []).filter(
    (target) =>
      !normalizedSearch ||
      `${target.group} ${target.model}`
        .toLocaleLowerCase(locale)
        .includes(normalizedSearch)
  )
  const resetFilters = () => {
    setSearch('')
  }

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
    let routeContent = <StatusCards targets={visibleTargets} locale={locale} />
    if (visibleTargets.length === 0) {
      routeContent = (
        <EmptyState
          icon={Search}
          title={t('No matching routes')}
          description={t('Try another search.')}
          action={
            <Button variant='outline' onClick={resetFilters}>
              {t('Reset filters')}
            </Button>
          }
          bordered
          className='min-h-[240px] rounded-xl'
        />
      )
    } else if (view === 'table') {
      routeContent = (
        <div className='overflow-hidden rounded-xl border'>
          <StatusTable targets={visibleTargets} locale={locale} />
        </div>
      )
    }
    content = (
      <div className='space-y-5 pb-2'>
        {query.isError && (
          <div
            role='alert'
            className='border-destructive/30 bg-destructive/5 text-destructive rounded-xl border px-4 py-3 text-sm'
          >
            {t('Refresh failed. Showing the last available status.')}
          </div>
        )}
        <StatusSummary targets={data.targets} locale={locale} />
        <section className='space-y-4' aria-label={t('Monitored routes')}>
          <div className='flex flex-col gap-3 xl:flex-row xl:items-center xl:justify-between'>
            <div>
              <h3 className='text-sm font-semibold'>
                {t('Group and model status')}
              </h3>
              <p className='text-muted-foreground mt-1 text-xs'>
                {t('Latest streaming observations for each group and model.')}
              </p>
            </div>
            <div className='flex flex-wrap items-center gap-2'>
              <InputGroup className='w-full sm:w-64'>
                <InputGroupAddon>
                  <Search className='size-3.5' aria-hidden='true' />
                </InputGroupAddon>
                <InputGroupInput
                  type='search'
                  aria-label={t('Search groups or models')}
                  placeholder={t('Search groups or models')}
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                />
              </InputGroup>
              <DataTableViewModeToggle value={view} onChange={setView} />
              {search && visibleTargets.length > 0 && (
                <Button variant='ghost' size='sm' onClick={resetFilters}>
                  {t('Reset filters')}
                </Button>
              )}
            </div>
          </div>
          {routeContent}
        </section>
      </div>
    )
  }

  return (
    <SectionPageLayout>
      <SectionPageLayout.Title>
        {t('API service status')}
      </SectionPageLayout.Title>
      <SectionPageLayout.Actions>
        <div className='text-muted-foreground hidden items-center gap-1.5 text-xs sm:flex'>
          <Clock3 className='size-3.5' aria-hidden='true' />
          <span>
            {t('Updated')} {formatTimestamp(data?.updated_at, locale)}
          </span>
        </div>
        <span className='text-muted-foreground text-xs'>
          {t('Auto-refreshing every {{seconds}}s', { seconds: 30 })}
        </span>
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
      <SectionPageLayout.Content>{content}</SectionPageLayout.Content>
    </SectionPageLayout>
  )
}
