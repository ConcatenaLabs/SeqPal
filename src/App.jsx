import { useEffect } from 'react'
import { Routes, Route, useLocation } from 'react-router-dom'
import Navbar from './components/Navbar'
import Footer from './components/Footer'
import IdNav from './components/IdNav'
import IdFooter from './components/IdFooter'
import Home from './pages/Home'
import Products from './pages/Products'
import Structures from './pages/Structures'
import Pricing from './pages/Pricing'
import SeqPalId from './pages/SeqPalId'
import Dashboard from './pages/Dashboard'
import Holdings from './pages/Holdings'
import IssuanceDetail from './pages/IssuanceDetail'
import NewIssuance from './pages/onboarding/NewIssuance'
import PortalSetup from './pages/portal/PortalSetup'
import InvestorPortal from './pages/portal/InvestorPortal'
import NotFound from './pages/NotFound'

const TITLES = {
  '/': 'SeqPal — Tokenization-as-a-Service on Bitcoin',
  '/products': 'Products — SeqPal',
  '/structures': 'Issuance Structures — SeqPal',
  '/pricing': 'Pricing — SeqPal',
  '/id': 'SeqPal ID',
  '/holdings': 'My holdings — SeqPal ID',
  '/dashboard': 'Issuer Dashboard — SeqPal',
  '/onboarding': 'New issuance — SeqPal',
}

function ScrollToTop() {
  const { pathname } = useLocation()
  useEffect(() => {
    window.scrollTo(0, 0)
    document.title =
      TITLES[pathname] ||
      (pathname.startsWith('/portal/')
        ? 'Investor portal'
        : pathname.includes('/portal')
          ? 'Placement portal setup — SeqPal'
          : pathname.startsWith('/issuance/')
            ? 'Issuance — SeqPal'
            : 'SeqPal')
  }, [pathname])
  return null
}

export default function App() {
  const { pathname } = useLocation()
  // Three site chromes:
  //  - "focused": full-screen flows (issuer onboarding wizard, portal setup, and
  //    the investor-facing placement portal on the issuer's own domain).
  //  - "id": the standalone SeqPal ID subsite (conceptually id.seqpal.io) — the
  //    investor + login surface, with NO issuance-business navigation.
  //  - default: the issuer-facing issuance platform (marketing + dashboard).
  const isFocused =
    pathname.startsWith('/onboarding') ||
    pathname.startsWith('/portal/') ||
    /^\/issuance\/[^/]+\/portal$/.test(pathname)
  const isIdSite = pathname === '/id' || pathname === '/holdings'

  return (
    <div className="flex min-h-screen flex-col">
      <ScrollToTop />
      {!isFocused && (isIdSite ? <IdNav /> : <Navbar />)}
      <main className="flex-1">
        <Routes>
          <Route path="/" element={<Home />} />
          <Route path="/products" element={<Products />} />
          <Route path="/structures" element={<Structures />} />
          <Route path="/pricing" element={<Pricing />} />
          <Route path="/id" element={<SeqPalId />} />
          <Route path="/dashboard" element={<Dashboard />} />
          <Route path="/holdings" element={<Holdings />} />
          <Route path="/issuance/:id" element={<IssuanceDetail />} />
          <Route path="/issuance/:id/portal" element={<PortalSetup />} />
          <Route path="/portal/:id" element={<InvestorPortal />} />
          <Route path="/onboarding" element={<NewIssuance />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
      </main>
      {!isFocused && (isIdSite ? <IdFooter /> : <Footer />)}
    </div>
  )
}
