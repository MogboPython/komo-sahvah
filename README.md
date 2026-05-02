# Common Server (Komo Sahvah)

Building this to learn details on how cloud platforms like [Vercel](https://vercel.com) and [Netlify](https://netlify.com) work. 
I got the inspiration from a Fullstack/Infra Engineer job advert put out by [Brimble](https://www.brimble.io).

> **Note:** This is an early stage proof of concept (PoC) and strictly for learning purposes. The goal is to demonstrate the possibilities and gauge interest.

## TODO
### Backend

- [x] Accept project files and save to temp folder (git urls/zip files)
- [ ] Railpack to build the app into a container image
- [ ] Run the container locally via Docker.
- [ ] Stream build and deploy logs to the UI in real time over SSE
- [ ] Configure Caddy to reverse-proxy a path or hostname to the running container. 

### Frontend
- [ ] Interface to submit git url or upload zipped file
- [ ] Stream build logs from backend