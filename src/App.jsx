import { useEffect } from 'react'
import { Routes, Route, useLocation } from 'react-router-dom'
import Navbar from './components/Navbar'
import Footer from './components/Footer'
import Home from './pages/Home'
import Products from './pages/Products'
import Structures from './pages/Structures'
import Pricing from './pages/Pricing'
import SeqPalId from './pages/SeqPalId'
import Dashboard from './pages/Dashboard'
import IssuanceDetail from './pages/IssuanceDetail'
import NewIssuance from './pages/onboarding/NewIssuance'
import PortalSetup from './pages/portal/PortalSetup'
import InvestorPortal from './pages/portal/InvestorPortal'
import NotFound from './pages/NotFound'

function ScrollToTop() {
  const { pathname } = useLocation()
  useEffect(() => window.scrollTo(0, 0), [pathname])
  return null
}

export default function App() {
  const { pathname } = useLocation()
  // The onboarding wizard and portal surfaces are focused, full-screen flows
  // without the marketing chrome.
  const isFocused =
    pathname.startsWith('/onboarding') ||
    pathname.startsWith('/portal/') ||
    /^\/issuance\/[^/]+\/portal$/.test(pathname)

  return (
    <div className="flex min-h-screen flex-col">
      <ScrollToTop />
      {!isFocused && <Navbar />}
      <main className="flex-1">
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/products" element={<Products />} />
          <Route path="/structures" element={<Structures />} />
          <Route path="/pricing" element={<Pricing />} />
          <Route path="/id" element={<SeqPalId />} />
          <Route path="/dashboard" element={<Dashboard />} />
          <Route path="/issuance/:id" element={<IssuanceDetail />} />
          <Route path="/issuance/:id/portal" element={<PortalSetup />} />
          <Route path="/portal/:id" element={<InvestorPortal />} />
          <Route path="/onboarding" element={<NewIssuance />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
      </main>
      {!isFocused && <Footer />}
    </div>
  )
}
