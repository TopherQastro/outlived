import React, { useCallback, useEffect, useState } from 'react'
import ReactDOM from 'react-dom'

import {
    AppBar,
    Box,
    Card,
    CardContent,
    CardHeader,
    Checkbox,
    CircularProgress,
    FormControlLabel,
    Link,
    Snackbar,
    ThemeProvider,
    Typography,
} from '@material-ui/core'
import { Alert } from '@material-ui/lab'
import {
    createTheme,
    makeStyles,
    Theme,
    useTheme,
} from '@material-ui/core/styles'

import { Figures } from './Figures'
import { TopBar } from './TopBar'
import { User } from './User'

import { post } from './post'
import { Data, FigureData, UserData } from './types'
import { tzname } from './tz'
import { daysInMonth } from './util'

// https://paletton.com/#uid=23+0u0k87oa2jC650sPbokcd+eW
const theme = createTheme({
    palette: {
        primary: {
            light: '#B8B8C3',
            main: '#8B8BA1',
            dark: '#6A6A87',
            contrastText: '#343453',
        },
        secondary: {
            light: '#FFFAED',
            main: '#E6DDC2',
            dark: '#C1B490',
            contrastText: '#776A43',
        },
    },
    typography: {
        button: {
            textTransform: 'none',
        },
    },
})

const useStyles = makeStyles((theme: Theme) => ({
    today: {
        backgroundColor: theme.palette.primary.light,
        borderWidth: '4px',
        color: theme.palette.primary.dark,
        fontSize: '1.2rem',
        margin: '1rem',
        textAlign: 'center',
        width: 'fit-content',
    },
}))

export const App = () => {
    const [alert, setAlert] = useState('')
    const [alertSeverity, setAlertSeverity] = useState<'error' | 'info'>('error')
    const [figures, setFigures] = useState<FigureData[]>([])
    const [loaded, setLoaded] = useState(false)
    const [today, setToday] = useState('')
    const [user, setUser] = useState<UserData | null>(null)

    // Added fame toggle state
    const [byPopularity, setByPopularity] = useState(false)

    const classes = useStyles()

    const getData = async () => {
        try {
            // Include byPopularity in the POST request
            const resp = await post('/s/data', { tzname: tzname(), byPopularity })
            const data = (await resp.json()) as Data
            if (!data.figures) {
                setAlert('Error: received no figures from server')
            } else {
                setFigures(data.figures)
                setToday(data.today)
                setUser(data.user)
                setLoaded(true)
            }

            // Queue a refetch of the data for when the date changes.

            const now = new Date()
            let y = now.getFullYear()
            let m = 1 + now.getMonth()
            let d = now.getDate()

            d++
            if (d > daysInMonth(y, m)) {
                d = 1
                m++
            }
            if (m > 12) {
                m = 1
                y++
            }

            const tomw = new Date(y, m - 1, d)

            // Reload happens at 1 second + up to 5 minutes into the new day (to avoid a stampede).
            window.setTimeout(
                getData,
                tomw.getTime() - now.getTime() + 1000 + Math.random() * 300000
            )
        } catch (error) {
            setAlert(`Error loading data: ${error.message}`)
        }
    }

    // Calling getData inside useEffect this way eliminates duplicate calls to the server.
    useEffect(() => {
        if (!loaded && !alert) {
            getData()
        }
    }, [loaded, alert, byPopularity])

    const setAlertAPI = (msg: string, severity?: 'error' | 'info') => {
        setAlertSeverity(severity || 'error')
        setAlert(msg)
    }

    return (
        <ThemeProvider theme={theme}>
            <TopBar user={user} setUser={setUser} setAlert={setAlertAPI} />
            <Snackbar open={!!alert} onClose={() => setAlert('')}>
                <Alert severity={alertSeverity}>{alert}</Alert>
            </Snackbar>
            {loaded ? (
                <>
                    <Box display='flex' justifyContent='center' m='auto'>
                        <Card className={classes.today} raised={true} variant='outlined'>
                            <CardContent>
                                <CardHeader title={`Today is ${today}`} />
                                {user ? (
                                    <>
                                        <Typography>
                                            You were born on {user.born}, which was{' '}
                                            {user.daysAlive.toLocaleString()} days ago
                                            <br />({user.yearsDaysAlive}).
                                        </Typography>
                                        <FormControlLabel
                                            control={
                                                <Checkbox
                                                    checked={byPopularity}
                                                    onChange={(e) => {
                                                        setByPopularity(e.target.checked)
                                                        setLoaded(false) // Triggers a refetch with new sort
                                                    }}
                                                    color="primary"
                                                />
                                            }
                                            label="Sort matches by fame"
                                            style={{ marginTop: '10px' }}
                                        />
                                    </>
                                ) : (
                                    undefined
                                )}
                            </CardContent>
                        </Card>
                    </Box>
                    <Figures
                        diedToday={figures}
                        outlived={user ? user.figures : undefined}
                    />
                    <Box alignItems='center' justifyContent='center' textAlign='center'>
                        <Typography paragraph={true} variant='caption'>
                            Data supplied by{' '}
                            <Link
                                target='_blank'
                                rel='noopener noreferrer'
                                href='https://en.wikipedia.org/'
                            >
                                Wikipedia
                            </Link>
                            , the free encyclopedia.
                        </Typography>
                        <Typography paragraph={true} variant='caption'>
                            Some graphic design elements supplied by Suzanne Glickstein.
                            Thanks Suze!
                        </Typography>
                        <Typography paragraph={true} variant='caption'>
                            Curious about how this site works? Read the source at{' '}
                            <Link
                                target='_blank'
                                rel='noopener noreferrer'
                                href='https://github.com/bobg/outlived/'
                            >
                                github.com/bobg/outlived
                            </Link>
                            !
                        </Typography>
                    </Box>
                </>
            ) : (
                <Box display='flex' justifyContent='center' m='2em'>
                    <CircularProgress />
                </Box>
            )}
        </ThemeProvider>
    )
}

ReactDOM.render(<App />, document.getElementById('root'))