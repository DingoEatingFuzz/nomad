import Head from 'next/head'

export default function DefaultHeadTags() {
  return (
    <Head>
      <title key="title">Nomad by HashiCorp</title>
      <meta charSet="utf-8" />
      <meta httpEquiv="x-ua-compatible" content="ie=edge" />
      {/* ref: https://www.phpied.com/minimum-viable-sharing-meta-tags/ */}
      <meta property="og:locale" content="en_US" />
      <meta property="og:type" content="website" />
      <meta
        property="og:site_name"
        content="Nomad by HashiCorp"
        key="og-name"
      />
      <meta name="twitter:site" content="@HashiCorp" />
      <meta name="twitter:card" content="summary_large_image" />
      <meta
        property="article:publisher"
        content="https://www.facebook.com/HashiCorp/"
      />
      <meta
        name="description"
        property="og:description"
        content="Nomad is a highly available, distributed, data-center aware cluster and application scheduler designed to support the modern datacenter with support for long-running services, batch jobs, and much more."
        key="description"
      />
      <meta
        property="og:image"
        content="https://www.nomadproject.io/img/og-image.png"
        key="image"
      />
      <link type="image/png" rel="icon" href="/favicon.ico" />
      <link rel="stylesheet" href="/css/nprogress.css"></link>
      <link
        href="https://fonts.googleapis.com/css?family=Open+Sans:300,400,600,700&display=swap"
        rel="stylesheet"
      />
    </Head>
  )
}
