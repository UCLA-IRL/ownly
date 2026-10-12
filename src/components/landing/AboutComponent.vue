<template>
  <div class="outer">
    <img alt="screenshot" class="screenshot" src="@/assets/about/screenshot.png" />

    <div class="title-big">Take back control of your Workspace</div>

    <div class="features">
      <div class="feature">
        <div class="description">
          <div class="name">Collaboration</div>

          Work together on files with your team in real-time. Ownly will automatically handle
          conflict resolution. You can invite as many people as you want to your workspace.
        </div>
        <div class="image">
          <img alt="screenshot" class="screenshot" src="@/assets/about/collaboration.png" />
        </div>
      </div>

      <div class="feature">
        <div class="description">
          <div class="name">Discussions</div>

          Continue conversations with your team in a dedicated space. You can create channels for
          different topics and invite people to join.
        </div>
        <div class="image">
          <img alt="screenshot" class="screenshot" src="@/assets/about/discussion.png" />
        </div>
      </div>

      <div class="feature">
        <div class="description">
          <div class="name">Local-First &amp; Peer-to-Peer</div>

          All data is stored locally on your device, and backed up to a data repository of your
          choice. Ownly works offline, and can synchronize data with other users without external
          control points.
        </div>
        <div class="image">
          <img alt="screenshot" class="screenshot" src="@/assets/about/local.png" />
        </div>
      </div>

      <div class="feature">
        <div class="description">
          <div class="name">Free &amp; Open Source</div>

          Powered by
          <a href="https://named-data.net" target="_blank">Named Data Networking</a> standards, a
          new data-centric architecture for the Internet. We build on 15+ years of research and
          development at various universities. All components of Ownly are open source and are
          available on <a href="https://github.com/UCLA-IRL/ownly" target="_blank">GitHub</a>.
        </div>
        <div class="image">
          <img alt="screenshot" class="screenshot" src="@/assets/about/ndn.png" />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts"></script>

<style scoped lang="scss">
.outer {
  display: flex;
  flex-direction: column;
  justify-content: center;
  align-items: center;
  width: 100%;
}

.screenshot {
  width: 70%;
  height: auto;
  max-width: 800px;
  filter: drop-shadow(0 0 3px rgba(0, 0, 0, 0.5));
  -webkit-filter: drop-shadow(0 0 3px rgba(0, 0, 0, 0.5));
  border-radius: 7px;
}

.title-big {
  font-size: 2.5rem;
  font-weight: 600;
  color: white;
  text-align: center;
  margin-top: 20px;
}

.features {
  margin-top: 20px;
  display: flex;
  flex-direction: column;
  width: 100%;
  justify-content: center;
  align-items: center;

  .feature {
    margin-top: 20px;
    display: flex;
    flex-direction: row;
    margin-bottom: 20px;
    color: white;

    $desc-width: 300px;
    $img-width: 400px;
    gap: min(max(calc(100vw - $desc-width - $img-width - 400px), 20px), 150px);

    .description {
      .name {
        font-size: 1.5rem;
        font-weight: 600;
        margin-bottom: 10px;
      }

      font-size: 1.15rem;
      font-weight: 300;
      margin-bottom: 10px;
      max-width: $desc-width;

      a {
        color: white;
        text-decoration: underline;
      }
    }

    .image {
      flex: 1;
      max-width: $img-width;
      > img {
        width: 100%;
      }
    }
  }
}

@media (max-width: 1023px) {
  .features {
    flex-direction: column;
    .feature {
      flex-direction: column;
      align-items: center;
      gap: 10px;
      text-align: center;
      .description {
        max-width: 75vw;
        text-align: center;
      }
    }
  }
}
</style>
