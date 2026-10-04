import type { ReactNode } from "react";
import { Link } from "react-router-dom";

// Public legal pages that Meta's App Review and Business Verification check: privacy policy,
// terms of service and data deletion instructions. They are English only and kept here rather
// than in the locale files, because the wording is a legal text, not interface copy.

export const COMPANY = "Ecogo AI Technologies Pvt Ltd";
export const LEGAL_EMAIL = "privacy@ecogo.co.in";
const UPDATED = "3 October 2026";

function LegalPage({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="legal-page">
      <header className="legal-head">
        <Link to="/" className="legal-logo"><img src="/ecogo-logo.webp" alt="Ecogo" /></Link>
        <LegalLinks />
      </header>
      <main className="legal-body">
        <h1>{title}</h1>
        <p className="muted">Last updated {UPDATED}</p>
        {children}
      </main>
      <footer className="legal-foot muted small">
        © {new Date().getFullYear()} {COMPANY}. Contact: <a href={`mailto:${LEGAL_EMAIL}`}>{LEGAL_EMAIL}</a>
      </footer>
    </div>
  );
}

export function LegalLinks() {
  return (
    <nav className="legal-links small" aria-label="Legal">
      <Link to="/privacy">Privacy policy</Link>
      <Link to="/terms">Terms of service</Link>
      <Link to="/data-deletion">Data deletion</Link>
    </nav>
  );
}

const mail = <a href={`mailto:${LEGAL_EMAIL}`}>{LEGAL_EMAIL}</a>;

export function Privacy() {
  return (
    <LegalPage title="Privacy policy">
      <p>
        This policy explains how {COMPANY} ("Ecogo", "we", "us"), a company registered in India, handles
        personal data in the Ecogo WhatsApp platform at whatsapp.ecogo.co.in, its API and related services
        (the "Service").
      </p>

      <h2>Our role</h2>
      <p>
        Businesses that sign up ("clients") use the Service to send and receive WhatsApp messages with their own
        customers. For their customers' data, the client decides why and how it is processed and is the Data
        Fiduciary under India's Digital Personal Data Protection Act, 2023; we process that data only on the
        client's instructions, as their Data Processor. For the account data of people who use the dashboard,
        and for billing data, we are the Data Fiduciary.
      </p>

      <h2>What we collect</h2>
      <ul>
        <li><b>Account data:</b> name, email address, password (stored only as a hash), two-step verification settings, workspace name, team roles and invitations.</li>
        <li><b>WhatsApp Business data from Meta:</b> when a client connects WhatsApp through Meta's Embedded Signup, we receive their business, WhatsApp Business Account and phone number IDs, display name, quality rating and messaging limits, and an access token that lets us act on that account. We do not receive the client's Facebook password.</li>
        <li><b>Messages and contacts:</b> messages, media, templates, contact names, phone numbers, tags, custom fields and opt-in records that clients and their customers exchange or upload, including chat history a client chooses to share from the WhatsApp Business app.</li>
        <li><b>Billing data:</b> plan, invoices, GST details and payment status. Card and bank details are entered with Razorpay and never reach us.</li>
        <li><b>Technical data:</b> IP address, browser, request logs, API usage and error logs, kept to run and secure the Service.</li>
      </ul>
      <p>We use only the cookies needed to keep you signed in and to protect forms against cross-site requests. We do not use advertising or analytics cookies.</p>

      <h2>How we use it</h2>
      <ul>
        <li>To provide the Service: deliver messages through Meta's WhatsApp Cloud API, show the inbox, run campaigns, chatbots and the features a client turns on.</li>
        <li>To secure accounts, prevent abuse and investigate errors.</li>
        <li>To bill clients and meet tax and accounting duties.</li>
        <li>To send service emails such as verification, invitations, invoices and notifications.</li>
      </ul>
      <p>We do not sell personal data and do not use clients' customer data for our own marketing or to train AI models.</p>

      <h2>Who we share it with</h2>
      <ul>
        <li><b>Meta Platforms</b>, to send and receive WhatsApp messages and manage templates, under Meta's own terms.</li>
        <li><b>Razorpay</b>, to take payments.</li>
        <li><b>Our email delivery provider</b>, to send service emails.</li>
        <li><b>Anthropic</b>, only when a client turns on AI replies: the customer's question and the client's knowledge base text are sent to generate an answer.</li>
        <li><b>Hetzner Online GmbH</b>, which hosts our servers in Germany.</li>
        <li>Authorities, when the law requires it.</li>
      </ul>

      <h2>Where it is stored</h2>
      <p>Data is stored on servers in Germany, in the European Union. Access tokens from Meta are encrypted at rest and are never shown in the browser or returned by our API.</p>

      <h2>How long we keep it</h2>
      <p>
        Messages are kept for as long as the client's workspace exists, or for the retention period the client sets
        in Settings. Account data is kept while the account is active and deleted when the workspace is deleted.
        Invoices are kept for as long as Indian tax law requires. Database backups are deleted after 30 days.
      </p>

      <h2>Your rights</h2>
      <p>
        You can ask to access, correct or erase your personal data, and to withdraw consent. If you are the
        customer of a business that uses Ecogo, please contact that business first, since it controls your data;
        we will help it answer you. To make a request, see <Link to="/data-deletion">Data deletion</Link> or write
        to {mail}. We answer within 30 days.
      </p>

      <h2>Grievance officer</h2>
      <p>For complaints about how we handle personal data, write to our grievance officer at {mail}. If you are not satisfied with our answer, you may complain to the Data Protection Board of India.</p>

      <h2>Children</h2>
      <p>The Service is for businesses and is not meant for anyone under 18.</p>

      <h2>Changes</h2>
      <p>We will post changes on this page and update the date above. For significant changes we will also email workspace owners.</p>
    </LegalPage>
  );
}

export function Terms() {
  return (
    <LegalPage title="Terms of service">
      <p>
        These terms are an agreement between {COMPANY} ("Ecogo", "we") and the business that creates a workspace
        ("you"). By creating an account or using the Service you accept them on behalf of your business.
      </p>

      <h2>The Service</h2>
      <p>
        Ecogo is software that connects your own WhatsApp Business Account to Meta's WhatsApp Cloud API, with a
        dashboard, team inbox, templates, contacts, campaigns, automation and a public API. We are a Meta Tech
        Provider: you own your WhatsApp Business Account and phone numbers, and you can disconnect them at any time.
      </p>

      <h2>Your account</h2>
      <ul>
        <li>You must give accurate details and keep passwords and API keys secret. You are responsible for what happens in your workspace.</li>
        <li>You decide who on your team has access and which role they have.</li>
      </ul>

      <h2>Using WhatsApp responsibly</h2>
      <ul>
        <li>You must follow the <a href="https://business.whatsapp.com/policy" target="_blank" rel="noreferrer">WhatsApp Business Messaging Policy</a>, the WhatsApp Commerce Policy and Meta's terms.</li>
        <li>You must have your customers' opt-in before you message them first, and honour opt-outs. Ecogo records STOP replies for you.</li>
        <li>You must not send spam, unlawful content or anything that infringes others' rights.</li>
        <li>Meta may limit or block a number for low quality or policy breaches. That is Meta's decision, not ours.</li>
      </ul>

      <h2>Fees</h2>
      <ul>
        <li><b>Meta's message fees</b> are charged by Meta to the payment method you add in Meta Business Suite. We do not collect them.</li>
        <li><b>Our subscription</b> is charged in Indian rupees through Razorpay, monthly in advance, for your plan and any extra seats. New workspaces start with a 14-day free trial. Prices include GST, and we issue GST invoices.</li>
        <li>If a payment fails or a plan ends, sending is paused until the plan is renewed. Fees already paid are not refunded, except where the law requires.</li>
      </ul>

      <h2>Your data</h2>
      <p>
        You own the messages, contacts and other content in your workspace. You give us permission to process it only
        to provide the Service, as described in our <Link to="/privacy">Privacy policy</Link>. Under India's Digital
        Personal Data Protection Act, 2023 you are the Data Fiduciary for your customers' data and we act as your Data
        Processor. A data processing agreement is available on request at {mail}.
      </p>

      <h2>Availability and changes</h2>
      <p>
        We work to keep the Service available but do not guarantee it will be uninterrupted, and it depends on Meta's
        platform. We may change features; if a change materially reduces what you pay for, we will tell you in advance.
      </p>

      <h2>Suspension and ending</h2>
      <p>
        You can stop using the Service at any time and ask us to delete your workspace. We may suspend or close a workspace that
        breaks these terms or Meta's policies, or that puts the Service or other clients at risk, and will tell you why
        unless the law prevents it. After closing, your data is deleted as described in the Privacy policy.
      </p>

      <h2>Liability</h2>
      <p>
        The Service is provided "as is". To the extent the law allows, we are not liable for indirect or consequential
        losses, lost profits or lost data, and our total liability is limited to the fees you paid us in the 12 months
        before the claim.
      </p>

      <h2>Law</h2>
      <p>These terms are governed by the laws of India, and the courts with jurisdiction over our registered office decide any dispute.</p>

      <h2>Contact</h2>
      <p>Questions about these terms: {mail}.</p>
    </LegalPage>
  );
}

export function DataDeletion() {
  return (
    <LegalPage title="Data deletion instructions">
      <p>You can ask {COMPANY} to delete your personal data in any of these ways.</p>

      <h2>If you connected WhatsApp to Ecogo through Facebook</h2>
      <ol>
        <li>Open Facebook and go to <b>Settings and privacy</b>, then <b>Settings</b>, then <b>Apps and websites</b>.</li>
        <li>Find <b>Ecogo</b> and choose <b>Remove</b>.</li>
        <li>Facebook sends us a deletion request and shows you a confirmation code and a link where you can follow its status.</li>
      </ol>

      <h2>If you have an Ecogo account</h2>
      <p>
        Email {mail} from the address you sign in with and say whether you want your user account or your whole
        workspace deleted. Only a workspace owner can ask for a workspace to be deleted. You can also disconnect
        your WhatsApp numbers yourself under Numbers.
      </p>

      <h2>If a business messaged you on WhatsApp through Ecogo</h2>
      <p>
        That business controls your data. Ask it to delete your messages and contact details, or reply STOP to stop
        its messages. You may also write to {mail} with the business's name and your phone number, and we will pass
        your request on and help it act on it.
      </p>

      <h2>What happens next</h2>
      <p>
        We confirm your request and complete it within 30 days. We delete your account data, messages, contacts and
        files, and remove the WhatsApp access token we held for your business. Copies in our backups are deleted when
        those backups expire, within 30 days. We keep invoices only as long as Indian tax law requires.
      </p>
    </LegalPage>
  );
}
