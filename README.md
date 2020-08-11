# Nomad Prototype UI [![Docker Image Version (latest by date)](https://img.shields.io/docker/v/dingoeatingfuzz/nomad-prototype-ui?sort=date)](https://hub.docker.com/r/dingoeatingfuzz/nomad-prototype-ui)

<p align="center" style="text-align:center;">
<img src="https://github.com/hashicorp/nomad/blob/19c404ca791d6ebe95a81738d7dc6623ab28564d/website/public/img/logo-hashicorp.svg" width="300" />
</p>

## Prototype UI Program

In order to make the best UI possible, the Nomad team is experimenting with prototype UI programs. For certain features we really want to research and get right, we'll be publishing frequent builds of just the web UI for the community to test and provide feedback on.

The first program will take place during August and September 2020 and will be focused around a cluster-wide topology visualization.

## Enrolling in the Prototype UI Program

No need to enroll! All prototype builds will be completely open source and accessible to all. See below for the various ways of running the prototypes.

Now, if you have feedback or questions, please comment on the [community forum thread for the program].

## Running the Prototype Locally

The prototype UI is a docker container that takes a Nomad API address as an environment variable. Run the following after updating the NOMAD_API value to be the address of your cluster:

```console
$ docker run -p 6464:6464 --env NOMAD_API=http://localhost:4646 dingoeatingfuzz/nomad-prototype-ui:topo-viz-0.0.0
```

## Running the Prototype for Your Team

The prototype UI can be safely run inside the Nomad cluster it is configure to interface with. In this way, as an operator you can deploy the prototype for your whole team to try out. Here is an example job file you can riff off of.

Bear in mind Nomad needs a router to expose any web service. This example uses [Traefik](https://learn.hashicorp.com/tutorials/nomad/load-balancing-traefik).

```hcl
job "nomad-prototype-ui" {
  datacenters = ["dc1"]

  task "nomad-prototype-ui" {
    driver = "docker"

    config {
      image = "dingoeatingfuzz/nomad-prototype-ui:topo-viz-0.0.0"

      port_map {
        ui = 6464
      }
    }

    env {
      NOMAD_API = "http://localhost:4646"
    }

    resources {
      cpu    = 500
      memory = 256

      network {
        mbits = 10
        port "ui" {}
      }
    }

    # This assumes Traefik is running in your cluster
    service {
      name = "nomad-prototype-ui"
      port = "ui"

      tags = [
        "traefik.enable=true",
        "traefik.http.routers.http.rule=Path(`/nomad-prototype-ui`)",
      ]

      check {
        type = "http"
        path = "/"
        interval = "30s"
        timeout = "2s"
      }
    }
  }
}
```

## Security

The prototype UI introduces strictly read-only features. It is a fork of the stable UI so existing write functionality is present. Since this makes no changes to Nomad core, nor does it make any new write requests, it is considered safe to run against production clusters.

Please treat the prototype UI just like you treat the stable UI when it comes to security.

## Privacy

The Nomad team really wants your feedback but we do not automatically capture or report any data. We understand how important it is for practitioners to control the environment their clusters are running in and we are extending that flexibility and trust to our prototype program.

If you have feedback you would like to share privately, please email Nomad engineering or get in touch with an existing HashiCorp contact you may have.

## Troubleshooting

### This doesn't work at all, I see a blank screen

If this happens, odds are you don't have CORS enabled in your Nomad server config. Add this to your server HCL config and restart the server:

```hcl
http_api_response_headers {
  "Access-Control-Allow-Origin" = "*"
  "Access-Control-Expose-Headers" = "*"
}
```

### What if I can't restart Nomad but I also don't have CORS enabled?

That's okay! It's a little trickier to setup, but it's still totally doable. [This gist](https://gist.github.com/DingoEatingFuzz/f0ab7279c9fd73ec783a15bbac0ba037) has a sample NGINX config and Docker Compose file that will setup a proxy between your Nomad API and the prototype UI.

1. Download the two files on the gist.
2. Modify `nginx.conf` to include the correct address of your Nomad cluster
3. Run `docker-compose up`.
4. Visit the prototype ui at `localhost:6464`.
