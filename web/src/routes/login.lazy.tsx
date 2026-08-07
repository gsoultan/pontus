import { createLazyFileRoute } from '@tanstack/react-router'
import { LoginPage } from '../auth/LoginPage'

export const Route = createLazyFileRoute('/login')({
  component: LoginPage,
})
