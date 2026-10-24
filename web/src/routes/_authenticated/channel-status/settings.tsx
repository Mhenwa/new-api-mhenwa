import { createFileRoute } from '@tanstack/react-router'

import { ChannelStatusAdminPage } from '@/features/channel-status/admin-page'

export const Route = createFileRoute('/_authenticated/channel-status/settings')(
  {
    component: ChannelStatusAdminPage,
  }
)
