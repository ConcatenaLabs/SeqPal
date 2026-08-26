import { useEffect } from 'react'
import { Routes, Route, Navigate, useLocation } from 'react-router-dom'
import Navbar from './components/Navbar'
import Footer from './components/Footer'
import IdNav from './components/IdNav'
import IdFooter from './components/IdFooter'
import WalletSignPrompt from './components/WalletSignPrompt'
import Home from './pages/Home'
import Products from './pages/Products'
import Structures from './pages/Structures'
import Pricing from './pages/Pricing'
import Verify from './pages/Verify'
import Docs from './pages/Docs'
import ActionClaim from './pages/ActionClaim'
import Faq from './pages/Faq'
import Legal from './pages/Legal'
import Privacy from './pages/Privacy'
import Status from './pages/Status'
import IdLanding from './pages/id/IdLanding'
import IdRegister from './pages/id/IdRegister'
import IdPassport from './pages/id/IdPassport'
import IdEntities from './pages/id/IdEntities'
import Dashboard from './pages/Dashboard'
import Holdings from './pages/Holdings'
import IssuanceDetail from './pages/IssuanceDetail'
import NewIssuance from './pages/onboarding/NewIssuance'
import PortalSetup from './pages/portal/PortalSetup'
import InvestorPortal from './pages/portal/InvestorPortal'
import NotFound from './pages/NotFound'

const TITLES = {
  '/': 'SeqPal · Tokenization-as-a-Service on Sequentia',
  '/products': 'Products · SeqPal',
  '/structures': 'Issuance Structures · SeqPal',
  '/pricing': 'Pricing · SeqPal',
  '/docs': 'Documentation · SeqPal',
  '/docs/verify': 'Verify independently · SeqPal',
  '/faq': 'FAQ · SeqPal',
  '/legal': 'Legal & Licensing · SeqPal',
  '/privacy': 'Privacy · SeqPal',
  '/status': 'Status · SeqPal',
  '/id': 'SeqPal ID',
  '/id/register': 'Register · SeqPal ID',
  '/id/passport': 'Passport · SeqPal ID',
  '/id/entities': 'Companies · SeqPal ID',
  '/holdings': 'My holdings · SeqPal ID',
  '/dashboard': 'Issuer Dashboard · SeqPal',
  '/onboarding': 'New issuance · SeqPal',
}

function VerifyRedirect() {
  const { search } = useLocation()
  return <Navigate to={{ pathname: '/docs/verify', search }} replace />
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
          ? 'Placement portal setup · SeqPal'
          : pathname.startsWith('/issuance/')
            ? 'Issuance · SeqPal'
            : pathname.startsWith('/actions/')
              ? 'Shareholder action · SeqPal'
              : 'SeqPal')
  }, [pathname])
  return null
}

export default function App() {
  const { pathname } = useLocation()
  // Three site chromes:
  //  - "focused": full-screen flows (issuer onboarding wizard, portal setup, and
  //    the investor-facing placement portal on the issuer's own domain).
  //  - "id": the standalone SeqPal ID subsite (conceptually id.seqpal.io) · the
  //    investor + login surface, with NO issuance-business navigation.
  //  - default: the issuer-facing issuance platform (marketing + dashboard).
  const isFocused =
    pathname.startsWith('/onboarding') ||
    pathname.startsWith('/portal/') ||
    /^\/issuance\/[^/]+\/portal$/.test(pathname)
  const isIdSite =
    pathname === '/id' ||
    pathname.startsWith('/id/') ||
    pathname === '/holdings' ||
    pathname.startsWith('/actions/')

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
          <Route path="/docs" element={<Docs />} />
          <Route path="/docs/verify" element={<Verify />} />
          {/* The verification explainer moved under Documentation; the old
              address keeps working, query string (?asset=…) included. */}
          <Route path="/verify" element={<VerifyRedirect />} />
          <Route path="/faq" element={<Faq />} />
          <Route path="/legal" element={<Legal />} />
          <Route path="/privacy" element={<Privacy />} />
          <Route path="/status" element={<Status />} />
          <Route path="/id" element={<IdLanding />} />
          <Route path="/id/register" element={<IdRegister />} />
          <Route path="/id/passport" element={<IdPassport />} />
          <Route path="/id/entities" element={<IdEntities />} />
          <Route path="/dashboard" element={<Dashboard />} />
          <Route path="/holdings" element={<Holdings />} />
          <Route path="/actions/:id" element={<ActionClaim />} />
          <Route path="/issuance/:id" element={<IssuanceDetail />} />
          <Route path="/issuance/:id/portal" element={<PortalSetup />} />
          <Route path="/portal/:id" element={<InvestorPortal />} />
          <Route path="/onboarding" element={<NewIssuance />} />
          <Route path="*" element={<NotFound />} />
        </Routes>
      </main>
      {!isFocused && (isIdSite ? <IdFooter /> : <Footer />)}
      {/* A linked wallet signs out of band, so the prompt has to be reachable
          from every surface that can ask for a signature. */}
      <WalletSignPrompt />
    </div>
  )
}
