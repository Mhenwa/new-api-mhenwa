import { createFileRoute, redirect } from '@tanstack/react-router'

import { ChannelStatusAdminPage } from '@/features/channel-status/admin-page'
import { ROLE } from '@/lib/roles'
import { useAuthStore } from '@/stores/auth-store'

export const Route = createFileRoute('/_authenticated/channel-status/settings')(
  {
    beforeLoad: () => {
      const user = useAuthStore.getState().auth.user
      if (!user || user.role < ROLE.ADMIN) {
        throw redirect({ to: '/dashboard' })
      }
    },
    component: ChannelStatusAdminPage,
  }
)
