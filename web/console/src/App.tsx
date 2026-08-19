import { lazy, Suspense } from 'react'
import { Route, Routes, Navigate, useLocation } from 'react-router-dom'
import { QueryClientProvider } from '@tanstack/react-query'
import { ToastContainer } from 'react-toastify'
import 'react-toastify/dist/ReactToastify.css'
import '@/styles/toast.css'
import { queryClient } from '@/lib/queryClient'
import { PrivateLayout } from './components/layout/PrivateLayout'
import AppLoadingScreen from './components/layout/AppLoadingScreen'
import ErrorBoundary from './components/ErrorBoundary'
import { SetupGate } from './components/SetupGate'
import { CoreTenantProvider } from './context/CoreTenantContext'

// The app shell stays eager; every route page is code-split so the initial
// bundle only carries the shell + the current route's chunk.
const DashboardPage = lazy(() => import('./pages/dashboard'))
const TenantsPage = lazy(() => import('./pages/tenants'))
const TenantForm = lazy(() => import('./pages/tenants/TenantForm'))
const TenantDetails = lazy(() => import('./pages/tenants/TenantDetails'))
const ProjectsPage = lazy(() => import('./pages/projects'))
const ProjectForm = lazy(() => import('./pages/projects/ProjectForm'))
const ProjectDetails = lazy(() => import('./pages/projects/ProjectDetails'))
const ServicesPage = lazy(() => import('./pages/services'))
const ServiceForm = lazy(() => import('./pages/services/ServiceForm'))
const ServiceDetails = lazy(() => import('./pages/services/ServiceDetails'))
const ProvidersPage = lazy(() => import('./pages/providers'))
const ProviderForm = lazy(() => import('./pages/providers/ProviderForm'))
const ProviderDetails = lazy(() => import('./pages/providers/ProviderDetails'))
const AgentsPage = lazy(() => import('./pages/agents'))
const AgentForm = lazy(() => import('./pages/agents/AgentForm'))
const AgentDetails = lazy(() => import('./pages/agents/AgentDetails'))
const ResourceForm = lazy(() => import('./pages/resources/ResourceForm'))
const ResourceDetails = lazy(() => import('./pages/resources/ResourceDetails'))
const SetupPage = lazy(() => import('./pages/setup/SetupPage'))
const NotFoundPage = lazy(() => import('./pages/not-found/NotFoundPage'))
const ServiceUnavailablePage = lazy(() => import('./pages/service-unavailable/ServiceUnavailablePage'))

function App() {
  const location = useLocation()

  return (
    <QueryClientProvider client={queryClient}>
      <CoreTenantProvider>
        <ErrorBoundary resetKey={`${location.pathname}${location.search}`}>
          <Suspense fallback={<AppLoadingScreen />}>
            <SetupGate>
            <Routes>
              <Route path="/" element={<Navigate to="/dashboard" replace />} />
              <Route path="/setup" element={<SetupPage />} />
              <Route path="/service-unavailable" element={<ServiceUnavailablePage />} />
              <Route element={<PrivateLayout fullWidth />}>
                <Route path="dashboard" element={<DashboardPage />} />

                <Route path="tenants" element={<TenantsPage />} />
                <Route path="tenants/create" element={<TenantForm />} />
                <Route path="tenants/:id" element={<TenantDetails />} />
                <Route path="tenants/:id/edit" element={<TenantForm />} />

                <Route path="projects" element={<ProjectsPage />} />
                <Route path="projects/create" element={<ProjectForm />} />
                <Route path="projects/:id" element={<ProjectDetails />} />
                <Route path="projects/:id/edit" element={<ProjectForm />} />
                <Route path="projects/:projectId/resources/create" element={<ResourceForm />} />

                <Route path="resources/:id" element={<ResourceDetails />} />
                <Route path="resources/:id/edit" element={<ResourceForm />} />

                <Route path="services" element={<ServicesPage />} />
                <Route path="services/create" element={<ServiceForm />} />
                <Route path="services/:id" element={<ServiceDetails />} />
                <Route path="services/:id/edit" element={<ServiceForm />} />

                <Route path="providers" element={<ProvidersPage />} />
                <Route path="providers/create" element={<ProviderForm />} />
                <Route path="providers/:id" element={<ProviderDetails />} />
                <Route path="providers/:id/edit" element={<ProviderForm />} />

                <Route path="agents" element={<AgentsPage />} />
                <Route path="agents/create" element={<AgentForm />} />
                <Route path="agents/:id" element={<AgentDetails />} />
                <Route path="agents/:id/edit" element={<AgentForm />} />

                <Route path="*" element={<NotFoundPage />} />
              </Route>
              <Route path="*" element={<NotFoundPage />} />
            </Routes>
            </SetupGate>
          </Suspense>
        </ErrorBoundary>
      </CoreTenantProvider>
      <ToastContainer
        position="bottom-right"
        autoClose={5000}
        hideProgressBar={true}
        newestOnTop={false}
        closeOnClick
        rtl={false}
        pauseOnFocusLoss
        draggable
        pauseOnHover
        theme="light"
      />
    </QueryClientProvider>
  )
}

export default App
