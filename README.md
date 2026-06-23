# Common Server (Komo Sahvah)

Building this to learn details on how cloud platforms like [Vercel](https://vercel.com) and [Netlify](https://netlify.com) work. 
I got the inspiration from a Fullstack/Infra Engineer job advert put out by [Brimble](https://www.brimble.io).

> **Note:** This is an early stage proof of concept (PoC) and strictly for learning purposes. The goal is to demonstrate the possibilities and gauge interest.

## TODO
### Backend

- [x] Accept project files and save to temp folder (git urls/zip files)
- [x] Railpack to build the app into a container image
- [x] Run the container locally via Docker.
- [x] Stream build and deploy logs to the UI in real time over SSE
- [ ] Configure Caddy to reverse-proxy a path or hostname to the running container. 

### Frontend
- [ ] Interface to submit git url or upload zipped file
- [ ] Stream build logs from backend

## Challenges Faced & Solutions
Here is a breakdown of the notable issues encountered during development and how they were resolved.

### 1. Buildkit Error
* **The Problem:** BUILDKIT_HOST environment variable is not set or running.

### 2. Heavy Images
* **The Problem:**  Image were too large on the first project I built.
