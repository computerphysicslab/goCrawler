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

#### About the author

Find me at https://www.linkedin.com/in/semanticwebarchitect/ or computerphysicslab@gmail.com

I am very interested on Text-Mining, Natural Language Processing, Named Entity Recognition and Health corpus and search
