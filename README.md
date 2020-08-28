goCrawler
=========

Text-mining topic relevant sources
----------------------------------

- [About goCrawler](#About-goCrawler)
- [Install](#install)
- [Run](#run)
- [Output](#output)
- [About the author](#About-the-author)

#### About goCrawler

goLang crawler restricted to only topic relevant/curated URLs. It includes token frequency analysis and NLP nGram detection

#### Install

By using go.mod needed libraries are automagically downloaded on first run.

#### Run

Create myTopic.yaml using covid-19.yaml or blockchain.yaml as template

```shell
    $ go run crawler.go <myTopic>
```

neutral.yaml is a non-topic crawling profile to get a non-topic english corpus

```shell
    $ go run crawler.go neutral
```

#### Output

Topic relevant text on logs/corpusCuratedText.log

Other interesting results produced by computerphysicslab/goCrawler:
- Most frequent nGrams for COVID-19.txt: https://gist.github.com/computerphysicslab/49c6366f501c81a36a2cd15e25c3d386
- 500 covid-19 keywords frequencies.txt: https://gist.github.com/computerphysicslab/e3fbbf6d8b3e5be9048f70f872467617
- 500 best COVID-19 relevant links.md: https://gist.github.com/computerphysicslab/86ca73169279f9af24d9739d53ed4e35

#### About the author

Find me at https://www.linkedin.com/in/semanticwebarchitect/ or computerphysicslab@gmail.com

I am very interested on Text-Mining, Natural Language Processing, Named Entity Recognition and building textual corpora and search apps for Health/Biomedical/Clinical domain
