package queries

import "strings"

const GetConfigQuery = `
query (
    $owner: String!,
    $repo: String!,
    $rootConfigFileExpression: String!,
    $githubConfigFileExpression: String!,
    $orgRootConfigFileExpression: String,
    $orgGithubConfigFileExpression: String
  ) {
  repository(owner: $owner, name: $repo) {
    rootConfigFile: object(expression: $rootConfigFileExpression) {
      ... on Blob {
        text
      }
    }
    githubConfigFile: object(expression: $githubConfigFileExpression) {
      ... on Blob {
        text
      }
    }
  }
  orgConfigRepo: repository(owner: $owner, name: ".github") {
    rootConfigFile: object(expression: $orgRootConfigFileExpression) {
      ... on Blob {
        text
      }
    }
    githubConfigFile: object(expression: $orgGithubConfigFileExpression) {
      ... on Blob {
        text
      }
    }
  }
}
`

const getEventInfoQueryTemplate = `
query GetEventInfo($owner: String!, $repo: String!, $PRNumber: Int!) {
  repository(owner: $owner, name: $repo) {
    mergeCommitAllowed
    rebaseMergeAllowed
    squashMergeAllowed
    deleteBranchOnMerge
    isPrivate
    pullRequest(number: $PRNumber) {
      id
      author {
        login
        type: __typename
        ... on User {
          databaseId
          name
        }
        ... on Bot {
          databaseId
        }
      }
      isDraft
      mergeStateStatus
      reviewDecision
      state
      mergeable
      isCrossRepository
      reviewRequests(first: 100) {
        nodes {
          asCodeOwner
          requestedReviewer {
            __typename
            ... on Actor {
              login
            }
            ... on Team {
              name
            }
          }
        }
      }
      reviewThreads(first: 100) {
        nodes {
          isCollapsed
        }
      }
      title
      body
      bodyText
      %(bodyHTMLQuery)s
      url
      reviews(first: 100) {
        nodes {
          createdAt
          state
          author {
            login
            type: __typename
          }
          authorAssociation
        }
        totalCount
      }
      baseRefName
      headRefName
      headRef {
        id
      }
      baseRef {
        name
        branchProtectionRule {
          requiresStatusChecks
          requiredStatusCheckContexts
          requiresStrictStatusChecks
          requiresCommitSignatures
          %(requiresConversationResolution)s
          restrictsPushes
          pushAllowances(first: 100) {
            nodes {
              actor {
                ... on App {
                  databaseId
                }
              }
            }
          }
        }
        rules(first: 100) {
          nodes {
            type
            parameters {
              ... on RequiredStatusChecksParameters {
                requiredStatusChecks {
                  context
                }
                strictRequiredStatusChecksPolicy
              }
              ... on PullRequestParameters {
                requiredReviewThreadResolution
              }
            }
            repositoryRuleset {
              bypassActors(first: 100) {
                nodes {
                  actor {
                    __typename
                    ... on App {
                      databaseId
                    }
                  }
                }
              }
            }
          }
        }
      }
      commitHistory: commits(last: 100) {
        nodes {
          commit {
            author {
              user {
                databaseId
                name
                login
                type: __typename
              }
            }
            parents {
              totalCount
            }
          }
        }
      }
      commits(last: 1) {
        nodes {
          commit {
            checkSuites(first: 100) {
              nodes {
                checkRuns(first: 100) {
                  nodes {
                    name
                    conclusion
                  }
                }
              }
            }
            oid
            signature {
              isValid
            }
            status {
              state
              contexts {
                context
                state
              }
            }
          }
        }
      }
      labels(first: 100) {
        nodes {
          name
        }
        totalCount
      }
    }
  }
  orgConfigRepo: repository(owner: $owner, name: ".github") {
    defaultBranchRef {
      name
    }
  }
}

`

// GetEventInfoQuery builds the "do-all-the-things" GraphQL query.
func GetEventInfoQuery(requiresConversationResolution bool, fetchBodyHTML bool) string {
	conversationResolution := ""
	if requiresConversationResolution {
		conversationResolution = "requiresConversationResolution"
	}
	bodyHTMLQuery := "bodyHTML: body"
	if fetchBodyHTML {
		bodyHTMLQuery = "bodyHTML"
	}
	return strings.NewReplacer(
		"%(requiresConversationResolution)s", conversationResolution,
		"%(bodyHTMLQuery)s", bodyHTMLQuery,
	).Replace(getEventInfoQueryTemplate)
}

const MergePRMutation = `
mutation merge($PRId: ID!, $SHA: GitObjectID!, $title: String, $body: String) {
  mergePullRequest(input: {pullRequestId: $PRId, expectedHeadOid: $SHA, commitHeadline: $title, commitBody: $body}) {
    clientMutationId
  }
}

`

const apiFeaturesQuery = `
query {
   __type(name:"BranchProtectionRule") {
      fields(includeDeprecated: true) {
         name
      }
   }
}
`
